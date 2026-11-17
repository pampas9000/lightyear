package lifecycle

import (
	"errors"
	"slices"
	"testing"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/task"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestDeleteTaskKeepsOutputsOtherTasksReuse(t *testing.T) {
	e := setup(t)
	src := e.source("a.png")
	a := e.submit(src.ID, 70)
	shared := e.complete(a.ID)
	b := e.submit(src.ID, 70) // reuses shared
	if j := e.job(b.ID); *j.OutputFileID != shared.ID {
		t.Fatal("setup: B does not reuse A's output")
	}
	c := e.submit(src.ID, 50)
	own := e.complete(c.ID)

	res, err := e.svc.DeleteTask(t.Context(), e.owner, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DeletedOutputs) != 0 || !slices.Equal(res.RetainedOutputs, []uuid.UUID{shared.ID}) {
		t.Fatalf("delete A: %+v", res)
	}
	if _, err := e.tasks.GetTask(t.Context(), e.owner, a.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("task A still there: %v", err)
	}
	var jobs int64
	e.db.Model(&models.Job{}).Where("task_id = ?", a.ID).Count(&jobs)
	if jobs != 0 {
		t.Fatalf("%d jobs of A left", jobs)
	}
	if got, _ := e.file(shared.ID); got.Status != models.FileUploaded || e.store.has(shared.Path) {
		t.Fatalf("shared output touched: %s", got.Status)
	}
	// B still downloads it and new tasks still reuse it.
	if j := e.job(b.ID); j.OutputFile == nil || j.OutputFile.Status != models.FileUploaded {
		t.Fatalf("B's output after deleting A: %+v", j.OutputFile)
	}

	// The last task using it takes it along.
	res, err = e.svc.DeleteTask(t.Context(), e.owner, b.ID)
	if err != nil || !slices.Equal(res.DeletedOutputs, []uuid.UUID{shared.ID}) {
		t.Fatalf("delete B: %+v %v", res, err)
	}
	if _, ok := e.file(shared.ID); ok || !e.store.has(shared.Path) {
		t.Fatal("last user of the output deleted but output kept")
	}
	if j := e.job(e.submit(src.ID, 70).ID); j.Status != models.JobPending {
		t.Fatalf("deleted output reused: %s", j.Status)
	}
	// Other tasks and the source are untouched.
	if got, _ := e.file(own.ID); got.Status != models.FileUploaded {
		t.Fatalf("C's output: %s", got.Status)
	}
	if got, _ := e.file(src.ID); got.Status != models.FileUploaded {
		t.Fatalf("source: %s", got.Status)
	}
}

func TestDeleteTaskRules(t *testing.T) {
	e := setup(t)
	src := e.source("a.png")
	active := e.submit(src.ID, 70)
	if _, err := e.svc.DeleteTask(t.Context(), e.owner, active.ID); !errors.Is(err, ErrTaskActive) {
		t.Fatalf("active task: %v", err)
	}
	if _, err := e.svc.DeleteTask(t.Context(), uuid.New(), active.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("other owner's task: %v", err)
	}
	if _, err := e.svc.DeleteTask(t.Context(), e.owner, uuid.New()); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("missing task: %v", err)
	}
	if _, err := e.tasks.CancelTask(t.Context(), e.owner, active.ID); err != nil {
		t.Fatal(err)
	}
	if res, err := e.svc.DeleteTask(t.Context(), e.owner, active.ID); err != nil || len(res.DeletedOutputs) != 0 {
		t.Fatalf("cancelled task: %+v %v", res, err)
	}
	if _, err := e.svc.DeleteTask(t.Context(), e.owner, active.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestDeleteFile(t *testing.T) {
	e := setup(t)
	src := e.source("a.png")
	tk := e.submit(src.ID, 70)
	out := e.complete(tk.ID)

	if _, err := e.svc.DeleteFile(t.Context(), uuid.New(), src.ID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("other owner's file: %v", err)
	}
	f, err := e.svc.DeleteFile(t.Context(), e.owner, src.ID)
	if err != nil || f.Status != models.FileDeleted {
		t.Fatalf("delete source: %v", err)
	}
	// The job still points at it, so the row stays with the status.
	if j := e.job(tk.ID); j.SourceFile == nil || j.SourceFile.Status != models.FileDeleted || j.OutputFile.Status != models.FileUploaded {
		t.Fatalf("job after deleting its source: %+v", j)
	}
	if got, _ := e.file(src.ID); got.PurgedAt == nil || !e.store.has(src.Path) {
		t.Fatal("source object not removed")
	}
	// Repeating is harmless; a deleted source can no longer be submitted.
	if f, err := e.svc.DeleteFile(t.Context(), e.owner, src.ID); err != nil || f.Status != models.FileDeleted {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := e.tasks.CreateTask(t.Context(), task.CreateTaskInput{OwnerID: e.owner,
		Items: []task.TaskItemInput{{FileID: src.ID}}, Options: []byte(`{"format":"webp"}`)}); !errors.Is(err, task.ErrFileNotFound) {
		t.Fatalf("submit deleted source: %v", err)
	}

	// Outputs can be deleted directly too.
	if f, err := e.svc.DeleteFile(t.Context(), e.owner, out.ID); err != nil || f.Status != models.FileDeleted {
		t.Fatalf("delete output: %v", err)
	}
	if j := e.job(tk.ID); j.OutputFile.Status != models.FileDeleted {
		t.Fatalf("output status %s", j.OutputFile.Status)
	}

	// An unfinished upload is handed to the upload cleanup.
	up := models.File{OwnerID: e.owner, Name: "u.png", Path: "uploads/x/u.png", Status: models.FileUploading}
	e.db.Create(&up)
	if f, err := e.svc.DeleteFile(t.Context(), e.owner, up.ID); err != nil || f.Status != models.FileAborting {
		t.Fatalf("delete upload: %v %s", err, f.Status)
	}
}
