package task

import (
	"errors"
	"sync"
	"testing"

	"transcoder/server/internal/execution"
	"transcoder/server/internal/models"
	"transcoder/server/internal/operation"
	"transcoder/server/internal/testutil"
	"transcoder/server/internal/transcode"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func uploadedFile(t *testing.T, db *gorm.DB, owner uuid.UUID) models.File {
	t.Helper()
	file := models.File{OwnerID: owner, Name: "source.png", Path: "uploads/" + uuid.NewString() + ".png", Status: "UPLOADED"}
	if err := db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	return file
}

func TestLegacyTaskCreationUsesTransactionalOutbox(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	file := uploadedFile(t, db, owner)
	s := NewService(db, nil) // Redis is intentionally unavailable.
	input := CreateTaskInput{OwnerID: owner, TargetFormat: "png", Items: []TaskItemInput{{FileID: file.ID}}, IdempotencyKey: new("one")}
	got, err := s.CreateTask(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Jobs) != 1 || len(got.Jobs[0].Inputs) != 1 || len(got.Jobs[0].Outputs) != 1 || got.Jobs[0].Operation != operation.Transcode {
		t.Fatalf("legacy task was not normalized: %+v", got)
	}
	var count int64
	db.Model(&models.OutboxEvent{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected one committed wakeup, got %d", count)
	}
	repeated, err := s.CreateTask(t.Context(), input)
	if err != nil || repeated.ID != got.ID {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	input.Params = transcode.Params{Engine: "other"}
	if _, err := s.CreateTask(t.Context(), input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected conflicting snapshot, got %v", err)
	}
}

func TestGenerateTaskHasZeroInputsAndMultipleOutputs(t *testing.T) {
	db := testutil.OpenDatabase(t)
	registry := execution.DefaultRegistry()
	registry["test-ai"] = execution.Executor{Stream: "test:ai:stream", Operations: []operation.Kind{operation.Generate, operation.Edit}}
	s := NewServiceWithExecutors(db, nil, registry)
	input := CreateTaskInput{OwnerID: uuid.New(), Jobs: []JobSpec{{
		Operation: operation.Generate, Executor: "test-ai", TargetFormat: "png",
		Params: operation.JSON(`{"model":"test","prompt":"sky","width":512,"height":512,"count":3}`),
	}}}
	got, err := s.CreateTask(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Jobs) != 1 || len(got.Jobs[0].Inputs) != 0 || len(got.Jobs[0].Outputs) != 3 || got.Jobs[0].InputFileID != nil {
		t.Fatalf("unexpected generated task: %+v", got.Jobs)
	}
	if _, err := NewService(db, nil).CreateTask(t.Context(), input); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatalf("uninstalled executor must be rejected, got %v", err)
	}
}

func TestEditInputsAreOwnerScopedAndRolesPreserved(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	source, mask, reference := uploadedFile(t, db, owner), uploadedFile(t, db, owner), uploadedFile(t, db, owner)
	registry := execution.Registry{"test-ai": {Stream: "test:ai", Operations: []operation.Kind{operation.Edit}}}
	s := NewServiceWithExecutors(db, nil, registry)
	input := CreateTaskInput{OwnerID: owner, Jobs: []JobSpec{{
		Operation: operation.Edit, Executor: "test-ai", TargetFormat: "png",
		Params: operation.JSON(`{"model":"test","prompt":"replace background"}`),
		Inputs: []InputSpec{{source.ID, "source"}, {mask.ID, "mask"}, {reference.ID, "reference"}},
	}}}
	got, err := s.CreateTask(t.Context(), input)
	if err != nil || len(got.Jobs[0].Inputs) != 3 {
		t.Fatalf("edit task: %v", err)
	}
	foreign := uploadedFile(t, db, uuid.New())
	input.Jobs[0].Inputs[1].FileID = foreign.ID
	if _, err := s.CreateTask(t.Context(), input); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("foreign input was accepted: %v", err)
	}
}

func TestConcurrentIdempotentCreationCreatesOneTask(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	file := uploadedFile(t, db, owner)
	s := NewService(db, nil)
	input := CreateTaskInput{OwnerID: owner, TargetFormat: "png", Items: []TaskItemInput{{file.ID}}, IdempotencyKey: new("concurrent")}
	var wg sync.WaitGroup
	results := make(chan uuid.UUID, 8)
	for range 8 {
		wg.Go(func() {
			got, err := s.CreateTask(t.Context(), input)
			if err != nil {
				t.Errorf("create: %v", err)
				return
			}
			results <- got.ID
		})
	}
	wg.Wait()
	close(results)
	var first uuid.UUID
	for id := range results {
		if first == uuid.Nil {
			first = id
		} else if first != id {
			t.Error("duplicate task created")
		}
	}
	var count int64
	db.Model(&models.OutboxEvent{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected one wakeup, got %d", count)
	}
}

func TestManualRetryRejectsActiveOrForeignJobs(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	file := uploadedFile(t, db, owner)
	s := NewService(db, nil)
	parent, err := s.CreateTask(t.Context(), CreateTaskInput{
		OwnerID: owner, TargetFormat: "png", Items: []TaskItemInput{{FileID: file.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := RetryTaskInput{OwnerID: owner, TaskID: parent.ID, JobIDs: []uuid.UUID{parent.Jobs[0].ID}}
	if _, err := s.RetryTask(t.Context(), input); !errors.Is(err, ErrJobNotRetryable) {
		t.Fatalf("active job was accepted for retry: %v", err)
	}
	input.JobIDs = []uuid.UUID{uuid.New()}
	if _, err := s.RetryTask(t.Context(), input); !errors.Is(err, ErrInvalidJobID) {
		t.Fatalf("foreign job was accepted for retry: %v", err)
	}
}

func TestLegacyFingerprintStillMatchesAfterUpgrade(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	file := uploadedFile(t, db, owner)
	s := NewService(db, nil)
	input := CreateTaskInput{
		OwnerID: owner, TargetFormat: "PNG", Items: []TaskItemInput{{FileID: file.ID}}, IdempotencyKey: new("legacy"),
	}
	parent, err := s.CreateTask(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&parent).Update("request_fingerprint", input.Fingerprint("PNG", input.Params)).Error; err != nil {
		t.Fatal(err)
	}
	got, err := s.CreateTask(t.Context(), input)
	if err != nil || got.ID != parent.ID {
		t.Fatalf("legacy idempotency replay broke after upgrade: %v", err)
	}
}
