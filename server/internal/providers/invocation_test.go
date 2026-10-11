package providers

import (
	"errors"
	"sync"
	"testing"

	"transcoder/server/internal/execution"
	"transcoder/server/internal/models"
	"transcoder/server/internal/operation"
	"transcoder/server/internal/services/orchestrator"
	taskservice "transcoder/server/internal/services/task"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
)

func TestAmbiguousSubmissionIsNotRepeatedAcrossAttempts(t *testing.T) {
	db := testutil.OpenDatabase(t)
	registry := execution.Registry{"test-ai": {Stream: "test:ai", Operations: []operation.Kind{operation.Generate}}}
	tasks := taskservice.NewServiceWithExecutors(db, nil, registry)
	task, err := tasks.CreateTask(t.Context(), taskservice.CreateTaskInput{OwnerID: uuid.New(), Jobs: []taskservice.JobSpec{{
		Operation: operation.Generate, Executor: "test-ai", TargetFormat: "png",
		Params: operation.JSON(`{"model":"test","prompt":"sky","width":512,"height":512}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	worker := orchestrator.NewService(db, registry)
	if err := worker.Dispatch(t.Context(), task.Jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	task, _ = tasks.GetTask(t.Context(), task.OwnerID, task.ID)
	job := task.Jobs[0]
	ledger := NewInvocations(db)
	invocation, err := ledger.Prepare(t.Context(), job.ID, *job.AttemptID, "test")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := ledger.Prepare(t.Context(), job.ID, *job.AttemptID, "test")
	if err != nil || repeated.ID != invocation.ID || repeated.IdempotencyKey != invocation.IdempotencyKey {
		t.Fatalf("invocation identity changed: %v", err)
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for range 2 {
		wg.Go(func() { outcomes <- ledger.BeginSubmit(t.Context(), invocation.ID, *job.AttemptID) })
	}
	wg.Wait()
	close(outcomes)
	granted, blocked := 0, 0
	for err := range outcomes {
		if err == nil {
			granted++
		} else if errors.Is(err, ErrReconciliationRequired) {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if granted != 1 || blocked != 1 {
		t.Fatalf("submission permission: granted=%d blocked=%d", granted, blocked)
	}
	if err := ledger.MarkUnknown(t.Context(), invocation.ID, "response lost"); err != nil {
		t.Fatal(err)
	}
	if err := ledger.BeginSubmit(t.Context(), invocation.ID, *job.AttemptID); !errors.Is(err, ErrReconciliationRequired) {
		t.Fatalf("ambiguous request was resubmitted: %v", err)
	}
	if _, err := tasks.CancelTask(t.Context(), task.OwnerID, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.RetryTask(t.Context(), taskservice.RetryTaskInput{OwnerID: task.OwnerID, TaskID: task.ID, RetryAll: true}); !errors.Is(err, taskservice.ErrJobNotRetryable) {
		t.Fatalf("cancel/retry bypassed unknown invocation: %v", err)
	}
	receipt := Receipt{RemoteTaskID: "remote-1", RequestID: "request-1"}
	if err := ledger.RecordReceipt(t.Context(), invocation.ID, receipt); err != nil {
		t.Fatalf("late receipt was lost: %v", err)
	}
	if err := ledger.RecordOutcome(t.Context(), invocation.ID, false, &Usage{CostMinor: 7, Currency: "USD"}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.RecordReceipt(t.Context(), invocation.ID, receipt); err != nil {
		t.Fatal(err)
	}
	var persisted models.ProviderInvocation
	db.First(&persisted, "id = ?", invocation.ID)
	if persisted.Phase != "COMPLETED" || len(persisted.Usage) == 0 {
		t.Fatal("late receipt regressed terminal invocation")
	}
	task, _ = tasks.GetTask(t.Context(), task.OwnerID, task.ID)
	if task.Status != models.TaskCancelled {
		t.Fatal("late external receipt revived cancelled task")
	}
}
