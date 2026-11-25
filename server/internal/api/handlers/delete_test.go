package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/task"
	"transcoder/server/internal/services/upload"
	"transcoder/server/internal/testutil"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type nopStore struct{}

func (nopStore) DeleteObject(context.Context, string, string) error { return nil }

func (nopStore) AbortMultipartUpload(context.Context, string, string, string) error { return nil }

func (nopStore) HeadObject(context.Context, string, string) (*s3.HeadObjectOutput, error) {
	return nil, &smithy.GenericAPIError{Code: "NotFound"}
}

func TestConsoleDeleteEndpoints(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	files := lifecycle.NewService(db, nopStore{}, "bucket", lifecycle.Days(30, 30))
	app := fiber.New()
	app.Use(asTestUser)
	api := app.Group("/api")
	NewTaskHandler(task.NewService(db, nil), files).Install(api)
	NewFileHandler(db, nil, "bucket", files).Install(api)

	src := models.File{OwnerID: owner, Name: "a.png", Path: "uploads/a.png", Status: models.FileUploaded}
	db.Create(&src)
	tk, err := task.NewService(db, nil).CreateTask(t.Context(), task.CreateTaskInput{OwnerID: owner,
		Items: []task.TaskItemInput{{FileID: src.ID}}, Options: []byte(`{"format":"webp"}`)})
	if err != nil {
		t.Fatal(err)
	}
	expired := models.File{OwnerID: owner, Name: "old.webp", Path: "outputs/old.webp", Status: models.FileExpired}
	db.Create(&expired)

	for _, step := range []struct {
		method, path string
		user         uuid.UUID
		status       int
	}{
		{http.MethodDelete, "/api/tasks/" + tk.ID.String(), uuid.New(), http.StatusNotFound},
		{http.MethodDelete, "/api/tasks/" + tk.ID.String(), owner, http.StatusConflict},
		{http.MethodDelete, "/api/files/" + src.ID.String(), owner, http.StatusConflict},
		{http.MethodDelete, "/api/files/" + src.ID.String(), uuid.New(), http.StatusNotFound},
		{http.MethodGet, "/api/files/" + expired.ID.String() + "/download-url", owner, http.StatusGone},
		{http.MethodPost, "/api/tasks/" + tk.ID.String() + "/cancel", owner, http.StatusOK},
		{http.MethodDelete, "/api/tasks/" + tk.ID.String(), owner, http.StatusOK},
		{http.MethodDelete, "/api/files/" + src.ID.String(), owner, http.StatusOK},
		{http.MethodGet, "/api/tasks/" + tk.ID.String(), owner, http.StatusNotFound},
	} {
		req := httptest.NewRequest(step.method, step.path, nil)
		req.Header.Set("Test-User", step.user.String())
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != step.status {
			t.Errorf("%s %s: got %d, want %d", step.method, step.path, res.StatusCode, step.status)
		}
	}
}

func TestConsoleDraftEndpoints(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	files := lifecycle.NewService(db, nopStore{}, "bucket", lifecycle.Days(30, 30))
	app := fiber.New()
	app.Use(asTestUser)
	NewDraftHandler(upload.NewService(db, nil, "bucket", files), files).Install(app.Group("/api"))

	now := time.Now()
	var drafts []models.File
	for i := range 3 {
		f := models.File{OwnerID: owner, Name: "d.png", Path: "uploads/" + uuid.NewString(), Status: models.FileUploaded, UploadedAt: &now}
		db.Create(&f)
		d := models.DraftFile{FileID: f.ID, OwnerID: owner}
		db.Create(&d)
		db.Model(&d).Update("created_at", now.Add(time.Duration(i)*time.Second))
		drafts = append(drafts, f)
	}
	call := func(method, path string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Test-User", owner.String())
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		_ = json.NewDecoder(res.Body).Decode(&body)
		return res.StatusCode, body
	}

	// Pages of two, newest first, follow the cursor.
	var seen []string
	cursor := ""
	for range 3 {
		status, body := call(http.MethodGet, "/api/drafts/list?limit=2&cursor="+url.QueryEscape(cursor))
		if status != http.StatusOK {
			t.Fatalf("list: %d %v", status, body)
		}
		data := body["data"].(map[string]any)
		for _, f := range data["files"].([]any) {
			seen = append(seen, f.(map[string]any)["id"].(string))
		}
		if cursor = data["cursor"].(string); cursor == "" {
			break
		}
	}
	want := []string{drafts[2].ID.String(), drafts[1].ID.String(), drafts[0].ID.String()}
	if !slices.Equal(seen, want) {
		t.Fatalf("listed %v, want %v", seen, want)
	}

	if status, _ := call(http.MethodPost, "/api/drafts/"+drafts[0].ID.String()+"/delete"); status != http.StatusOK {
		t.Fatalf("delete draft: %d", status)
	}
	var f models.File
	if err := db.First(&f, "id = ?", drafts[0].ID).Error; err == nil {
		t.Fatalf("file of a deleted draft kept: %s", f.Status)
	}
	if status, body := call(http.MethodPost, "/api/drafts/"+drafts[0].ID.String()+"/delete"); status != http.StatusNotFound || body["code"] != "error.file.not_found" {
		t.Fatalf("second delete: %d %v", status, body)
	}
}
