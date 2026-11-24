package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/storage"
	"transcoder/server/internal/services/upload"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
)

// Calling complete before the PUT reached storage (or after the store
// rejected it, e.g. a checksum mismatch) is a client error, not a 500 that
// echoes the store's response.
func TestCompleteBeforeObjectIsUploadedIsConflict(t *testing.T) {
	e := setup(t)
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // HEAD: no such object
	}))
	t.Cleanup(store.Close)
	s3 := storage.NewS3Service(s3sdk.New(s3sdk.Options{
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider("id", "secret", ""),
		BaseEndpoint: aws.String(store.URL),
		UsePathStyle: true,
	}))
	e.h.uploads = upload.NewService(e.db, s3, "bucket", lifecycle.NewService(e.db, s3, "bucket", lifecycle.Policy{}))
	key := e.key()

	res, body := e.do(http.MethodPost, "/v1/uploads", key, `{"filename":"a.png","size":1024}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create upload: %d %s", res.StatusCode, body.Message)
	}
	up := decode[Upload](t, body.Data)

	res, body = e.do(http.MethodPost, "/v1/uploads/"+up.File.ID.String()+"/complete", key, "")
	if res.StatusCode != http.StatusConflict || body.Code != "error.param.invalid" || strings.Contains(string(body.Details), "StatusCode") {
		t.Fatalf("complete without object: %d %s %s %s", res.StatusCode, body.Code, body.Message, body.Details)
	}
	var f models.File
	if err := e.db.First(&f, "id = ?", up.File.ID).Error; err != nil || f.Status != "UPLOADING" {
		t.Fatalf("file after failed complete: %+v %v", f, err)
	}
}

// A storage failure is a 500 whose body carries no internal error text; the
// error goes to the log.
func TestCompleteStorageFailureDoesNotEchoError(t *testing.T) {
	e := setup(t)
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden) // HEAD: e.g. a revoked store key
	}))
	t.Cleanup(store.Close)
	s3 := storage.NewS3Service(s3sdk.New(s3sdk.Options{
		Region:           "auto",
		Credentials:      credentials.NewStaticCredentialsProvider("id", "secret", ""),
		BaseEndpoint:     aws.String(store.URL),
		UsePathStyle:     true,
		RetryMaxAttempts: 1,
	}))
	e.h.uploads = upload.NewService(e.db, s3, "bucket", lifecycle.NewService(e.db, s3, "bucket", lifecycle.Policy{}))
	key := e.key()

	res, body := e.do(http.MethodPost, "/v1/uploads", key, `{"filename":"a.png","size":1024}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create upload: %d %s", res.StatusCode, body.Message)
	}
	up := decode[Upload](t, body.Data)

	res, body = e.do(http.MethodPost, "/v1/uploads/"+up.File.ID.String()+"/complete", key, "")
	if res.StatusCode != http.StatusInternalServerError || body.Code != "error.internal" || len(body.Details) != 0 ||
		strings.Contains(body.Message, "403") || strings.Contains(body.Message, "head object") {
		t.Fatalf("complete with failing store: %d %s %q %s", res.StatusCode, body.Code, body.Message, body.Details)
	}
}

// Completing an upload schedules the file's retention right away.
func TestCompleteSetsExpiresAt(t *testing.T) {
	e := setup(t) // 30-day retention
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(store.Close)
	s3 := storage.NewS3Service(s3sdk.New(s3sdk.Options{
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider("id", "secret", ""),
		BaseEndpoint: aws.String(store.URL),
		UsePathStyle: true,
	}))
	e.h.uploads = upload.NewService(e.db, s3, "bucket", lifecycle.NewService(e.db, s3, "bucket", lifecycle.Days(30, 30)))
	key := e.key()

	res, body := e.do(http.MethodPost, "/v1/uploads", key, `{"filename":"a.png","size":1024}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create upload: %d %s", res.StatusCode, body.Message)
	}
	up := decode[Upload](t, body.Data)

	res, body = e.do(http.MethodPost, "/v1/uploads/"+up.File.ID.String()+"/complete", key, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("complete: %d %s %s", res.StatusCode, body.Code, body.Message)
	}
	got := decode[File](t, body.Data)
	var f models.File
	if err := e.db.First(&f, "id = ?", up.File.ID).Error; err != nil {
		t.Fatal(err)
	}
	want := f.UploadedAt.Add(30 * 24 * time.Hour)
	if f.ExpiresAt == nil || !f.ExpiresAt.Equal(want) || got.ExpiresAt == nil || !got.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at: stored %v, returned %v, want %v", f.ExpiresAt, got.ExpiresAt, want)
	}
}
