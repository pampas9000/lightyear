package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/task"
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
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", c.Get("Test-User"))
		return c.Next()
	})
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
