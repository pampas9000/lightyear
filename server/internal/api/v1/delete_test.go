package v1

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/apikey"
	"transcoder/server/internal/services/lifecycle"

	"github.com/google/uuid"
)

func TestDeleteRequiresWriteScope(t *testing.T) {
	e := setup(t)
	read := e.key(apikey.ScopeTasksRead)
	file := e.uploaded("a.png")
	for _, path := range []string{"/v1/tasks/" + uuid.NewString(), "/v1/files/" + file.ID.String()} {
		res, body := e.do(http.MethodDelete, path, read, "")
		if res.StatusCode != http.StatusForbidden || body.Code != "error.auth.insufficient_scope" {
			t.Errorf("DELETE %s with tasks:read: %d %s", path, res.StatusCode, body.Code)
		}
	}
	var f models.File
	e.db.First(&f, "id = ?", file.ID)
	if f.Status != models.FileUploaded {
		t.Fatalf("file changed by a forbidden request: %s", f.Status)
	}
}

func TestDeleteMissingOrForeignIsNotFound(t *testing.T) {
	e := setup(t)
	key := e.key()
	// Another user's task and file.
	stranger := uuid.New()
	foreignFile := models.File{OwnerID: stranger, Name: "x.png", Path: "uploads/x.png", Status: models.FileUploaded}
	e.db.Create(&foreignFile)
	foreignTask := models.Task{OwnerID: stranger, Status: models.TaskCompleted}
	e.db.Create(&foreignTask)

	cases := map[string]string{
		"/v1/tasks/" + uuid.NewString():        "error.task.not_found",
		"/v1/tasks/" + foreignTask.ID.String(): "error.task.not_found",
		"/v1/files/" + uuid.NewString():        "error.file.not_found",
		"/v1/files/" + foreignFile.ID.String(): "error.file.not_found",
	}
	for path, code := range cases {
		res, body := e.do(http.MethodDelete, path, key, "")
		if res.StatusCode != http.StatusNotFound || body.Code != code {
			t.Errorf("DELETE %s: %d %s, want 404 %s", path, res.StatusCode, body.Code, code)
		}
	}
	if res, body := e.do(http.MethodDelete, "/v1/tasks/nope", key, ""); res.StatusCode != http.StatusBadRequest || body.Code != "error.param.invalid" {
		t.Errorf("bad id: %d %s", res.StatusCode, body.Code)
	}
	var n int64
	e.db.Model(&models.Task{}).Where("id = ?", foreignTask.ID).Count(&n)
	if n != 1 || len(*e.deleted) != 0 {
		t.Fatal("foreign resources touched")
	}
}

func TestDeleteUnfinishedTaskIsConflict(t *testing.T) {
	e := setup(t)
	key := e.key()
	file := e.uploaded("a.png")
	_, body := e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 70))
	id := decode[Task](t, body.Data).ID.String()

	res, body := e.do(http.MethodDelete, "/v1/tasks/"+id, key, "")
	if res.StatusCode != http.StatusConflict || body.Code != "error.task.conflict" {
		t.Fatalf("delete pending task: %d %s", res.StatusCode, body.Code)
	}
	// Its source is busy too.
	res, body = e.do(http.MethodDelete, "/v1/files/"+file.ID.String(), key, "")
	if res.StatusCode != http.StatusConflict || body.Code != "error.file.conflict" {
		t.Fatalf("delete busy source: %d %s", res.StatusCode, body.Code)
	}
	// Cancel first, then delete.
	if res, _ := e.do(http.MethodPost, "/v1/tasks/"+id+"/cancel", key, ""); res.StatusCode != http.StatusOK {
		t.Fatal("cancel")
	}
	if res, _ := e.do(http.MethodDelete, "/v1/tasks/"+id, key, ""); res.StatusCode != http.StatusOK {
		t.Fatalf("delete cancelled task: %d", res.StatusCode)
	}
	if res, _ := e.do(http.MethodGet, "/v1/tasks/"+id, key, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted task still readable: %d", res.StatusCode)
	}
}

func TestDeleteTaskRemovesOnlyItsOwnOutputs(t *testing.T) {
	e := setup(t)
	key := e.key()
	file := e.uploaded("a.png")
	_, body := e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 70))
	a := decode[Task](t, body.Data).ID
	shared := e.complete(a)
	_, body = e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 70))
	b := decode[Task](t, body.Data)
	if b.Jobs[0].Status != models.JobCompleted || b.Jobs[0].OutputFile.ID != shared.ID {
		t.Fatal("setup: B does not reuse A's output")
	}
	_, body = e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 50))
	c := decode[Task](t, body.Data).ID
	own := e.complete(c)

	// C's output is its own: deleted with it.
	res, body := e.do(http.MethodDelete, "/v1/tasks/"+c.String(), key, "")
	got := decode[lifecycle.TaskDeletion](t, body.Data)
	if res.StatusCode != http.StatusOK || !slices.Equal(got.DeletedOutputs, []uuid.UUID{own.ID}) || len(got.RetainedOutputs) != 0 {
		t.Fatalf("delete C: %d %+v", res.StatusCode, got)
	}
	if !slices.Contains(*e.deleted, own.Path) {
		t.Fatal("C's output object not deleted")
	}
	// A's output is reused by B: kept.
	res, body = e.do(http.MethodDelete, "/v1/tasks/"+a.String(), key, "")
	got = decode[lifecycle.TaskDeletion](t, body.Data)
	if res.StatusCode != http.StatusOK || len(got.DeletedOutputs) != 0 || !slices.Equal(got.RetainedOutputs, []uuid.UUID{shared.ID}) {
		t.Fatalf("delete A: %d %+v", res.StatusCode, got)
	}
	_, body = e.do(http.MethodGet, "/v1/tasks/"+b.ID.String(), key, "")
	job := decode[Task](t, body.Data).Jobs[0]
	if job.OutputFile == nil || job.OutputFile.Status != models.FileUploaded || job.OutputFile.DownloadURL == "" {
		t.Fatalf("B's output after deleting A: %+v", job.OutputFile)
	}
	if slices.Contains(*e.deleted, shared.Path) {
		t.Fatal("shared output object deleted")
	}
}

func TestExpiredOutputHasNoDownloadURL(t *testing.T) {
	e := setup(t)
	key := e.key()
	file := e.uploaded("a.png")
	_, body := e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 70))
	id := decode[Task](t, body.Data).ID
	out := e.complete(id)
	e.db.Model(&models.File{}).Where("id = ?", out.ID).Update("expires_at", time.Now().Add(-time.Minute))
	if _, err := e.h.files.Sweep(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}

	res, body := e.do(http.MethodGet, "/v1/tasks/"+id.String(), key, "")
	task := decode[Task](t, body.Data)
	job := task.Jobs[0]
	if res.StatusCode != http.StatusOK || task.Status != models.TaskCompleted || job.Status != models.JobCompleted {
		t.Fatalf("GET: %d %s", res.StatusCode, body.Data)
	}
	if job.OutputFile == nil || job.OutputFile.Status != models.FileExpired || job.OutputFile.DownloadURL != "" || job.OutputFile.DownloadExpiresAt != nil {
		t.Fatalf("expired output: %+v", job.OutputFile)
	}
	// Retrying a job whose source still exists produces a new output.
	if res, _ := e.do(http.MethodPost, "/v1/tasks/"+id.String()+"/retry", key, `{"retry_all":true}`); res.StatusCode != http.StatusAccepted {
		t.Fatalf("retry: %d", res.StatusCode)
	}
}

func TestDeleteFile(t *testing.T) {
	e := setup(t)
	key := e.key()
	file := e.uploaded("a.png")
	res, body := e.do(http.MethodDelete, "/v1/files/"+file.ID.String(), key, "")
	got := decode[File](t, body.Data)
	if res.StatusCode != http.StatusOK || got.ID != file.ID || got.Status != models.FileDeleted {
		t.Fatalf("delete: %d %s", res.StatusCode, body.Data)
	}
	if !slices.Contains(*e.deleted, file.Path) {
		t.Fatal("object not deleted")
	}
	// The row went with the object (nothing referenced it); repeating is a 404.
	if res, _ := e.do(http.MethodDelete, "/v1/files/"+file.ID.String(), key, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete: %d", res.StatusCode)
	}
	if res, body := e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 70)); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("submit deleted file: %d %s", res.StatusCode, body.Code)
	}
}
