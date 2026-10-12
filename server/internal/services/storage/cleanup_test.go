package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/testutil"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// mockRoundTripper intercepts S3 HTTP calls and returns mocked responses
type mockRoundTripper struct {
	roundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFunc(req)
}

func setupTestDB(t *testing.T) *gorm.DB {
	return testutil.OpenDatabase(t)
}

func TestCleanupStateClaimAndSelfHealing(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 1. Create a dummy user
	user := models.User{
		Username:     "test_cleanup_user_" + uuid.New().String()[:8],
		Email:        "test_cleanup_" + uuid.New().String()[:8] + "@example.com",
		PasswordHash: "hash",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Define S3 mock client that returns NoSuchUpload error, but HeadObject succeeds (object exists in S3)
	// This simulates the race condition where DB update failed but upload succeeded.
	mockRt := &mockRoundTripper{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			// S3 AbortMultipartUpload uses DELETE method
			if req.Method == "DELETE" && req.URL.Query().Get("uploadId") != "" {
				// Return NoSuchUpload XML body
				xmlBody := `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchUpload</Code><Message>The specified upload does not exist.</Message></Error>`
				resp := &http.Response{
					StatusCode: http.StatusNotFound,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(xmlBody)),
				}
				resp.Header.Set("Content-Type", "application/xml")
				return resp, nil
			} else if req.Method == "HEAD" {
				// HeadObject succeeds (Object exists)
				resp := &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       http.NoBody,
				}
				resp.Header.Set("Content-Length", "5000")
				resp.Header.Set("Content-Type", "video/mp4")
				return resp, nil
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       http.NoBody,
			}, nil
		},
	}

	s3Client := s3sdk.NewFromConfig(aws.Config{
		Region: "auto",
		HTTPClient: &http.Client{
			Transport: mockRt,
		},
	})
	s3Service := NewS3Service(s3Client)

	// Create an expired file in DB (status: UPLOADING, UploadID present, expired)
	expiredAt := time.Now().Add(-1 * time.Hour)
	file := models.File{
		OwnerID:         user.ID,
		Name:            "healing_test.mp4",
		Path:            "uploads/" + user.ID.String() + "/healing_test.mp4",
		Status:          "UPLOADING",
		UploadID:        "some_upload_id",
		UploadExpiresAt: &expiredAt,
		Size:            0,
	}
	if err := db.Create(&file).Error; err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	db.Create(&models.DraftFile{
		FileID:  file.ID,
		OwnerID: user.ID,
	})

	// Run cleanup
	runCleanup(ctx, db, s3Service, "test-bucket")

	// Verify database record has self-healed to UPLOADED
	var cleanedFile models.File
	if err := db.Where("id = ?", file.ID).First(&cleanedFile).Error; err != nil {
		t.Fatalf("file record should not be deleted, but error: %v", err)
	}

	if cleanedFile.Status != "UPLOADED" {
		t.Errorf("expected file status to self-heal to UPLOADED, got %s", cleanedFile.Status)
	}
	if cleanedFile.Size != 5000 {
		t.Errorf("expected healed size to be 5000, got %d", cleanedFile.Size)
	}
	if cleanedFile.UploadedAt == nil {
		t.Errorf("expected UploadedAt to be populated on healed record")
	}

	// Clean up DB records for this test
	db.Where("file_id = ?", cleanedFile.ID).Delete(&models.DraftFile{})
	db.Delete(&cleanedFile)
	db.Delete(&user)
}

func TestCleanupStaleUploadedFiles(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create user
	user := models.User{
		Username:     "test_stale_user_" + uuid.New().String()[:8],
		Email:        "test_stale_" + uuid.New().String()[:8] + "@example.com",
		PasswordHash: "hash",
	}
	db.Create(&user)

	// Mock S3 delete always succeeding
	mockRt := &mockRoundTripper{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			if req.Method == "DELETE" {
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Body:       http.NoBody,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       http.NoBody,
			}, nil
		},
	}
	s3Client := s3sdk.NewFromConfig(aws.Config{
		Region: "auto",
		HTTPClient: &http.Client{
			Transport: mockRt,
		},
	})
	s3Service := NewS3Service(s3Client)

	// Case 1: Unsubmitted file uploaded 8 days ago (Should be cleaned)
	uploadedAt := time.Now().Add(-8 * 24 * time.Hour)
	staleFile := models.File{
		OwnerID:    user.ID,
		Name:       "stale.mp4",
		Path:       "uploads/" + user.ID.String() + "/stale.mp4",
		Status:     "UPLOADED",
		UploadedAt: &uploadedAt,
	}
	db.Create(&staleFile)
	db.Create(&models.DraftFile{
		Base: models.Base{
			CreatedAt: uploadedAt,
		},
		FileID:  staleFile.ID,
		OwnerID: user.ID,
	})

	// Case 2: Unsubmitted file uploaded 1 day ago (Should NOT be cleaned)
	recentUploadedAt := time.Now().Add(-1 * 24 * time.Hour)
	recentFile := models.File{
		OwnerID:    user.ID,
		Name:       "recent.mp4",
		Path:       "uploads/" + user.ID.String() + "/recent.mp4",
		Status:     "UPLOADED",
		UploadedAt: &recentUploadedAt,
	}
	db.Create(&recentFile)
	db.Create(&models.DraftFile{
		Base: models.Base{
			CreatedAt: recentUploadedAt,
		},
		FileID:  recentFile.ID,
		OwnerID: user.ID,
	})

	// Case 3: Submitted file uploaded 8 days ago (Should NOT be cleaned)
	submittedFile := models.File{
		OwnerID:    user.ID,
		Name:       "submitted.mp4",
		Path:       "uploads/" + user.ID.String() + "/submitted.mp4",
		Status:     "UPLOADED",
		UploadedAt: &uploadedAt,
	}
	db.Create(&submittedFile)

	// Create job using the submitted file to reference it
	job := models.Job{
		OwnerID:      user.ID,
		Status:       models.JobPending,
		SourceFileID: submittedFile.ID,
	}
	db.Create(&job)

	// Case 4: Being a job's output also protects a stale draft.
	relationFile := models.File{
		OwnerID:    user.ID,
		Name:       "relation-input.mp4",
		Path:       "uploads/" + user.ID.String() + "/relation-input.mp4",
		Status:     "UPLOADED",
		UploadedAt: &uploadedAt,
	}
	db.Create(&relationFile)
	db.Create(&models.DraftFile{
		Base:    models.Base{CreatedAt: uploadedAt},
		FileID:  relationFile.ID,
		OwnerID: user.ID,
	})
	relationJob := models.Job{OwnerID: user.ID, Status: models.JobCompleted, OutputFileID: &relationFile.ID}
	db.Create(&relationJob)

	// Run cleanup
	runCleanup(ctx, db, s3Service, "test-bucket")

	// Verify Case 1 (staleFile) was deleted
	var f1 models.File
	if err := db.Where("id = ?", staleFile.ID).First(&f1).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected staleFile to be deleted, but found record: %+v", f1)
	}

	// Verify Case 2 (recentFile) was NOT deleted
	var f2 models.File
	if err := db.Where("id = ?", recentFile.ID).First(&f2).Error; err != nil {
		t.Errorf("expected recentFile to remain, but got error: %v", err)
	}

	// Verify Case 3 (submittedFile) was NOT deleted
	var f3 models.File
	if err := db.Where("id = ?", submittedFile.ID).First(&f3).Error; err != nil {
		t.Errorf("expected submittedFile to remain, but got error: %v", err)
	}
	var f4 models.File
	if err := db.Where("id = ?", relationFile.ID).First(&f4).Error; err != nil {
		t.Errorf("expected relationFile to remain, but got error: %v", err)
	}

	// Clean up database
	db.Delete(&job)
	db.Where("file_id = ?", staleFile.ID).Delete(&models.DraftFile{})
	db.Delete(&staleFile)
	db.Where("file_id = ?", recentFile.ID).Delete(&models.DraftFile{})
	db.Delete(&recentFile)
	db.Where("file_id = ?", submittedFile.ID).Delete(&models.DraftFile{})
	db.Delete(&submittedFile)
	db.Delete(&relationJob)
	db.Where("file_id = ?", relationFile.ID).Delete(&models.DraftFile{})
	db.Delete(&relationFile)
	db.Delete(&user)
}

func TestCleanupDeletingAndAbortingFiles(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create user
	user := models.User{
		Username:     "test_del_user_" + uuid.New().String()[:8],
		Email:        "test_del_" + uuid.New().String()[:8] + "@example.com",
		PasswordHash: "hash",
	}
	db.Create(&user)

	var s3Deleted []string
	var s3Aborted []string
	var s3FailPath string

	mockRt := &mockRoundTripper{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			path := req.URL.Path
			if s3FailPath != "" && strings.Contains(path, s3FailPath) {
				return nil, errors.New("simulated S3 transient error")
			}

			if req.Method == "DELETE" {
				if req.URL.Query().Get("uploadId") != "" {
					s3Aborted = append(s3Aborted, path)
				} else {
					s3Deleted = append(s3Deleted, path)
				}
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Body:       http.NoBody,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       http.NoBody,
			}, nil
		},
	}
	s3Client := s3sdk.NewFromConfig(aws.Config{
		Region: "auto",
		HTTPClient: &http.Client{
			Transport: mockRt,
		},
	})
	s3Service := NewS3Service(s3Client)

	// Case 1: File in status DELETING (Should be deleted successfully from S3 & DB)
	deletingFile := models.File{
		OwnerID: user.ID,
		Name:    "deleting.mp4",
		Path:    "uploads/" + user.ID.String() + "/deleting.mp4",
		Status:  "DELETING",
	}
	db.Create(&deletingFile)
	db.Create(&models.DraftFile{
		FileID:  deletingFile.ID,
		OwnerID: user.ID,
	})

	// Case 2: File in status ABORTING (Should abort multipart upload, then delete from DB)
	abortingFile := models.File{
		OwnerID:  user.ID,
		Name:     "aborting.mp4",
		Path:     "uploads/" + user.ID.String() + "/aborting.mp4",
		Status:   "ABORTING",
		UploadID: "test_upload_id",
	}
	db.Create(&abortingFile)
	db.Create(&models.DraftFile{
		FileID:  abortingFile.ID,
		OwnerID: user.ID,
	})

	// Case 3: S3 Delete fails (Should keep DB records)
	failFile := models.File{
		OwnerID: user.ID,
		Name:    "fail.mp4",
		Path:    "uploads/" + user.ID.String() + "/fail.mp4",
		Status:  "DELETING",
	}
	db.Create(&failFile)
	db.Create(&models.DraftFile{
		FileID:  failFile.ID,
		OwnerID: user.ID,
	})
	s3FailPath = "fail.mp4"

	// Run cleanup
	runCleanup(ctx, db, s3Service, "test-bucket")

	// Verify Case 1 (deletingFile) was deleted from DB and S3 delete was called
	var f1 models.File
	if err := db.Where("id = ?", deletingFile.ID).First(&f1).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected deletingFile to be deleted, but found: %+v", f1)
	}
	var df1 models.DraftFile
	if err := db.Where("file_id = ?", deletingFile.ID).First(&df1).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected draft association for deletingFile to be deleted, but found: %+v", df1)
	}

	// Verify Case 2 (abortingFile) was deleted from DB and S3 abort/delete was called
	var f2 models.File
	if err := db.Where("id = ?", abortingFile.ID).First(&f2).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected abortingFile to be deleted, but found: %+v", f2)
	}
	var df2 models.DraftFile
	if err := db.Where("file_id = ?", abortingFile.ID).First(&df2).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected draft association for abortingFile to be deleted, but found: %+v", df2)
	}

	// Verify Case 3 (failFile) was NOT deleted from DB
	var f3 models.File
	if err := db.Where("id = ?", failFile.ID).First(&f3).Error; err != nil {
		t.Errorf("expected failFile to remain in DB, but got error: %v", err)
	}
	var df3 models.DraftFile
	if err := db.Where("file_id = ?", failFile.ID).First(&df3).Error; err != nil {
		t.Errorf("expected draft association for failFile to remain in DB, but got error: %v", err)
	}

	// Verify S3 calls were recorded correctly
	if len(s3Deleted) != 1 || !strings.Contains(s3Deleted[0], "deleting.mp4") {
		t.Errorf("expected deleting.mp4 to be deleted, got: %v", s3Deleted)
	}
	if len(s3Aborted) != 1 || !strings.Contains(s3Aborted[0], "aborting.mp4") {
		t.Errorf("expected aborting.mp4 upload to be aborted, got: %v", s3Aborted)
	}

	// Clean up database
	db.Where("file_id = ?", deletingFile.ID).Delete(&models.DraftFile{})
	db.Delete(&deletingFile)
	db.Where("file_id = ?", abortingFile.ID).Delete(&models.DraftFile{})
	db.Delete(&abortingFile)
	db.Where("file_id = ?", failFile.ID).Delete(&models.DraftFile{})
	db.Delete(&failFile)
	db.Delete(&user)
}

// A single PUT has no multipart upload to abort. An expired row is healed when
// the object exists (the browser never called complete) and deleted otherwise.
func TestCleanupExpiredSinglePut(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	user := models.User{
		Username:     "test_single_put_" + uuid.New().String()[:8],
		Email:        "test_single_put_" + uuid.New().String()[:8] + "@example.com",
		PasswordHash: "hash",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	existingKey := "uploads/" + user.ID.String() + "/finished.png"
	var aborts int
	s3Client := s3sdk.NewFromConfig(aws.Config{
		Region: "auto",
		HTTPClient: &http.Client{Transport: &mockRoundTripper{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				if req.Method == "DELETE" && req.URL.Query().Get("uploadId") != "" {
					aborts++
				}
				if req.Method == "HEAD" && strings.HasSuffix(req.URL.Path, existingKey) {
					resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}
					resp.Header.Set("Content-Length", "1234")
					return resp, nil
				}
				return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: http.NoBody}, nil
			},
		}},
	})
	s3Service := NewS3Service(s3Client)

	expiredAt := time.Now().Add(-time.Hour)
	finished := models.File{OwnerID: user.ID, Name: "finished.png", Path: existingKey, Status: "UPLOADING", UploadExpiresAt: &expiredAt}
	abandoned := models.File{OwnerID: user.ID, Name: "abandoned.png", Path: "uploads/" + user.ID.String() + "/abandoned.png", Status: "UPLOADING", UploadExpiresAt: &expiredAt}
	for _, f := range []*models.File{&finished, &abandoned} {
		if err := db.Create(f).Error; err != nil {
			t.Fatal(err)
		}
		db.Create(&models.DraftFile{FileID: f.ID, OwnerID: user.ID})
	}

	runCleanup(ctx, db, s3Service, "test-bucket")

	if aborts != 0 {
		t.Errorf("single PUT rows must not abort a multipart upload, got %d aborts", aborts)
	}
	var healed models.File
	if err := db.Where("id = ?", finished.ID).First(&healed).Error; err != nil {
		t.Fatalf("finished upload should be kept: %v", err)
	}
	if healed.Status != "UPLOADED" || healed.Size != 1234 || healed.UploadedAt == nil {
		t.Errorf("finished upload not healed: status=%s size=%d uploaded_at=%v", healed.Status, healed.Size, healed.UploadedAt)
	}
	var count int64
	db.Model(&models.File{}).Where("id = ?", abandoned.ID).Count(&count)
	if count != 0 {
		t.Errorf("abandoned upload row should be deleted")
	}
}
