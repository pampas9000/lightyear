package orchestrator_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"transcoder/server/internal/execution"
	"transcoder/server/internal/models"
	"transcoder/server/internal/operation"
	"transcoder/server/internal/services/orchestrator"
	taskservice "transcoder/server/internal/services/task"
	taskqueue "transcoder/server/internal/task"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type fixture struct {
	db     *gorm.DB
	tasks  *taskservice.Service
	worker *orchestrator.Service
	task   *models.Task
}

func setup(t *testing.T, count int) fixture {
	t.Helper()
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	var items []taskservice.TaskItemInput
	for range count {
		file := models.File{OwnerID: owner, Status: "UPLOADED", Name: "input.png", Path: "uploads/" + uuid.NewString() + ".png", MimeType: "image/png"}
		if err := db.Create(&file).Error; err != nil {
			t.Fatal(err)
		}
		items = append(items, taskservice.TaskItemInput{FileID: file.ID})
	}
	tasks := taskservice.NewService(db, nil)
	parent, err := tasks.CreateTask(t.Context(), taskservice.CreateTaskInput{OwnerID: owner, Items: items, TargetFormat: "png"})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{db, tasks, orchestrator.NewService(db, execution.DefaultRegistry()), parent}
}

func (f fixture) dispatch(t *testing.T, id uuid.UUID) models.Job {
	t.Helper()
	if err := f.worker.Dispatch(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	return f.get(t, id)
}

func (f fixture) get(t *testing.T, id uuid.UUID) models.Job {
	t.Helper()
	got, err := f.tasks.GetTask(t.Context(), f.task.OwnerID, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range got.Jobs {
		if job.ID == id {
			return job
		}
	}
	t.Fatal("job missing")
	return models.Job{}
}

func completed(job models.Job) taskqueue.ResultPayload {
	return taskqueue.ResultPayload{SchemaVersion: "1.0", JobID: job.ID.String(), AttemptID: job.AttemptID.String(),
		Status:   taskqueue.ResultStatusCompleted,
		Metadata: map[string]any{"output_size_bytes": int64(1234), "mime_type": "image/png", "width": 512, "height": 256}}
}

func heartbeat(job models.Job) taskqueue.ResultPayload {
	return taskqueue.ResultPayload{SchemaVersion: "1.0", JobID: job.ID.String(), AttemptID: job.AttemptID.String(),
		Status: taskqueue.ResultStatusProcessing, Progress: new(0)}
}

func TestConcurrentDispatchClaimsOneAttempt(t *testing.T) {
	f := setup(t, 1)
	id := f.task.Jobs[0].ID
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := f.worker.Dispatch(t.Context(), id); err != nil {
				t.Errorf("dispatch: %v", err)
			}
		})
	}
	wg.Wait()
	job := f.get(t, id)
	if job.Status != models.JobProcessing || job.AttemptID == nil {
		t.Fatalf("job was not claimed: %+v", job)
	}
	var attempts []models.JobAttempt
	f.db.Where("job_id = ?", id).Find(&attempts)
	if len(attempts) != 1 {
		t.Fatalf("expected one attempt, got %d", len(attempts))
	}
	var manifest []taskqueue.Output
	if err := json.Unmarshal(attempts[0].OutputManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(manifest[0].Path, "/attempts/"+job.AttemptID.String()+"/") ||
		manifest[0].Path == job.OutputFile.Path {
		t.Fatalf("output is not isolated: %+v", manifest)
	}
}

func TestCompletionPublishesOnlyAcceptedAttempt(t *testing.T) {
	f := setup(t, 1)
	job := f.dispatch(t, f.task.Jobs[0].ID)
	if err := f.worker.ProcessResult(t.Context(), completed(job)); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.ProcessResult(t.Context(), heartbeat(job)); err != nil {
		t.Fatal(err)
	}
	accepted := f.get(t, job.ID)
	if accepted.Status != models.JobCompleted || accepted.Progress != 100 ||
		accepted.OutputFile.Width != 512 || accepted.OutputFile.Height != 256 ||
		!strings.Contains(accepted.OutputFile.Path, job.AttemptID.String()) {
		t.Fatalf("completion was not published: %+v", accepted)
	}
	if _, err := f.tasks.RetryTask(t.Context(), taskservice.RetryTaskInput{
		OwnerID: f.task.OwnerID, TaskID: f.task.ID, JobIDs: []uuid.UUID{job.ID},
	}); err != nil {
		t.Fatal(err)
	}
	retried := f.dispatch(t, job.ID)
	if retried.AttemptID == nil || *retried.AttemptID == *job.AttemptID {
		t.Fatal("manual retry reused attempt")
	}
	if err := f.worker.ProcessResult(t.Context(), completed(job)); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, job.ID); got.Status != models.JobProcessing || *got.AttemptID != *retried.AttemptID {
		t.Fatal("stale result changed the new attempt")
	}
	if err := f.worker.ProcessResult(t.Context(), completed(retried)); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, job.ID); !strings.Contains(got.OutputFile.Path, retried.AttemptID.String()) {
		t.Fatal("new output path was not published")
	}
}

func TestArtifactPathCannotEscapeAttemptManifest(t *testing.T) {
	f := setup(t, 1)
	job := f.dispatch(t, f.task.Jobs[0].ID)
	result := completed(job)
	result.Artifacts = []taskqueue.Artifact{{FileID: job.OutputFileID.String(), Path: "uploads/other-owner.png", Size: 50, MimeType: "image/png"}}
	if err := f.worker.ProcessResult(t.Context(), result); !errors.Is(err, orchestrator.ErrInvalidResult) {
		t.Fatalf("expected manifest rejection, got %v", err)
	}
	got := f.get(t, job.ID)
	if got.Status != models.JobProcessing || got.OutputFile.Status != "GENERATING" {
		t.Fatal("invalid artifact changed persisted state")
	}
}

func TestHeartbeatAndCompletionCannotRegressState(t *testing.T) {
	f := setup(t, 1)
	job := f.dispatch(t, f.task.Jobs[0].ID)
	var wg sync.WaitGroup
	for _, result := range []taskqueue.ResultPayload{heartbeat(job), completed(job), heartbeat(job), completed(job)} {
		wg.Go(func() {
			if err := f.worker.ProcessResult(t.Context(), result); err != nil {
				t.Errorf("result: %v", err)
			}
		})
	}
	wg.Wait()
	if got := f.get(t, job.ID); got.Status != models.JobCompleted || got.Progress != 100 || got.OutputFile.Status != "UPLOADED" {
		t.Fatal("concurrent heartbeat regressed completion")
	}
}

func TestCancelAndCompletionRemainConsistent(t *testing.T) {
	f := setup(t, 1)
	job := f.dispatch(t, f.task.Jobs[0].ID)
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := f.tasks.CancelTask(t.Context(), f.task.OwnerID, f.task.ID); err != nil {
			t.Errorf("cancel: %v", err)
		}
	})
	wg.Go(func() {
		if err := f.worker.ProcessResult(t.Context(), completed(job)); err != nil {
			t.Errorf("complete: %v", err)
		}
	})
	wg.Wait()
	got := f.get(t, job.ID)
	parent, err := f.tasks.GetTask(t.Context(), f.task.OwnerID, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	switch got.Status {
	case models.JobCompleted:
		if got.OutputFile.Status != "UPLOADED" || parent.Status != models.TaskCompleted {
			t.Fatal("completed job has inconsistent file/task")
		}
	case models.JobCancelled:
		if got.OutputFile.Status != "FAILED" || parent.Status != models.TaskCancelled {
			t.Fatal("cancelled job has inconsistent file/task")
		}
	default:
		t.Fatalf("unexpected state: %s", got.Status)
	}
}

func TestParallelSiblingCompletionAggregatesTask(t *testing.T) {
	f := setup(t, 4)
	var jobs []models.Job
	for _, job := range f.task.Jobs {
		jobs = append(jobs, f.dispatch(t, job.ID))
	}
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Go(func() {
			if err := f.worker.ProcessResult(t.Context(), completed(job)); err != nil {
				t.Errorf("completion: %v", err)
			}
		})
	}
	wg.Wait()
	parent, err := f.tasks.GetTask(t.Context(), f.task.OwnerID, f.task.ID)
	if err != nil || parent.Status != models.TaskCompleted {
		t.Fatalf("task aggregation: %v, %+v", err, parent)
	}
}

func TestQueuedRecoveryRedeliversWithoutNewAttempt(t *testing.T) {
	f := setup(t, 1)
	job := f.dispatch(t, f.task.Jobs[0].ID)
	now := time.Now()
	f.db.Model(&models.Job{}).Where("id = ?", job.ID).Update("lease_expires_at", now.Add(-time.Second))
	if err := f.worker.Recover(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	got := f.get(t, job.ID)
	if *got.AttemptID != *job.AttemptID || got.RetryCount != 0 {
		t.Fatal("queue backlog exhausted a retry or changed attempt")
	}
}

func TestExpiredRunningAttemptBacksOffAndRejectsLateResult(t *testing.T) {
	f := setup(t, 1)
	job := f.dispatch(t, f.task.Jobs[0].ID)
	if err := f.worker.ProcessResult(t.Context(), heartbeat(job)); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	f.db.Model(&models.Job{}).Where("id = ?", job.ID).Update("lease_expires_at", now.Add(-time.Second))
	if err := f.worker.Recover(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	pending := f.get(t, job.ID)
	if pending.Status != models.JobPending || pending.RetryCount != 1 || pending.NextRunAt == nil || !pending.NextRunAt.After(now) {
		t.Fatalf("missing retry backoff: %+v", pending)
	}
	if err := f.worker.Dispatch(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, job.ID); got.Status != models.JobPending {
		t.Fatal("premature message bypassed retry schedule")
	}
	f.db.Model(&models.Job{}).Where("id = ?", job.ID).Update("next_run_at", now.Add(-time.Second))
	retried := f.dispatch(t, job.ID)
	if err := f.worker.ProcessResult(t.Context(), completed(job)); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, job.ID); got.Status != models.JobProcessing || *got.AttemptID != *retried.AttemptID {
		t.Fatal("old attempt overwrote recovered job")
	}
}

func TestRecoveryRechecksRenewedLease(t *testing.T) {
	f := setup(t, 1)
	job := f.dispatch(t, f.task.Jobs[0].ID)
	if err := f.worker.ProcessResult(t.Context(), heartbeat(job)); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.Recover(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, job.ID); got.RetryCount != 0 || got.Status != models.JobProcessing {
		t.Fatal("healthy lease was recovered")
	}
}

func TestGeneratedArtifactsPublishAllOutputsAtomically(t *testing.T) {
	db := testutil.OpenDatabase(t)
	registry := execution.Registry{"test-ai": {Stream: "test:ai", Operations: []operation.Kind{operation.Generate}}}
	tasks := taskservice.NewServiceWithExecutors(db, nil, registry)
	parent, err := tasks.CreateTask(t.Context(), taskservice.CreateTaskInput{
		OwnerID: uuid.New(), Jobs: []taskservice.JobSpec{{
			Operation: operation.Generate, Executor: "test-ai", TargetFormat: "png",
			Params: operation.JSON(`{"model":"test","prompt":"sky","width":512,"height":512,"count":3}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	worker := orchestrator.NewService(db, registry)
	if err := worker.Dispatch(t.Context(), parent.Jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	var attempt models.JobAttempt
	if err := db.First(&attempt, "job_id = ?", parent.Jobs[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	var manifest []taskqueue.Output
	if err := json.Unmarshal(attempt.OutputManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	var artifacts []taskqueue.Artifact
	for _, output := range manifest {
		artifacts = append(artifacts, taskqueue.Artifact{
			FileID: output.FileID, Path: output.Path, Size: 100, MimeType: "image/png", Width: 512, Height: 512,
		})
	}
	result := taskqueue.ResultPayload{
		SchemaVersion: "1.1", JobID: attempt.JobID.String(), AttemptID: attempt.ID.String(),
		Status: taskqueue.ResultStatusCompleted, Artifacts: artifacts[:2],
	}
	if err := worker.ProcessResult(t.Context(), result); !errors.Is(err, orchestrator.ErrInvalidResult) {
		t.Fatalf("partial result must be rejected: %v", err)
	}
	result.Artifacts = artifacts
	result.Artifacts[2].Path = "uploads/forged.png"
	if err := worker.ProcessResult(t.Context(), result); !errors.Is(err, orchestrator.ErrInvalidResult) {
		t.Fatalf("forged last artifact must roll back earlier files: %v", err)
	}
	got, err := tasks.GetTask(t.Context(), parent.OwnerID, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range got.Jobs[0].Outputs {
		if output.File.Status != "GENERATING" || output.File.Size != 0 {
			t.Fatal("rejected multi-artifact result partially published output")
		}
	}
	result.Artifacts[2].Path = manifest[2].Path
	if err := worker.ProcessResult(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	got, err = tasks.GetTask(t.Context(), parent.OwnerID, parent.ID)
	if err != nil || got.Status != models.TaskCompleted {
		t.Fatalf("multi-output completion: %v, %+v", err, got)
	}
	for i, output := range got.Jobs[0].Outputs {
		if output.File.Status != "UPLOADED" || output.File.Path != manifest[i].Path {
			t.Fatal("accepted multi-artifact result was not fully published")
		}
	}
}

func TestRetryAfterIsBoundedAndRetryLimitTerminatesJob(t *testing.T) {
	f := setup(t, 1)
	job := f.dispatch(t, f.task.Jobs[0].ID)
	start := time.Now()
	result := taskqueue.ResultPayload{
		SchemaVersion: "1.0", JobID: job.ID.String(), AttemptID: job.AttemptID.String(),
		Status: taskqueue.ResultStatusFailed, ErrorCode: "rate_limited",
		Retryable: new(true), RetryAfterSeconds: new(7200),
	}
	if err := f.worker.ProcessResult(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	got := f.get(t, job.ID)
	if got.NextRunAt == nil || got.NextRunAt.Before(start.Add(time.Hour)) || got.NextRunAt.After(time.Now().Add(time.Hour)) {
		t.Fatalf("Retry-After not clamped to one hour: %+v", got)
	}
	for retry := 1; retry <= job.MaxRetries; retry++ {
		if err := f.db.Model(&models.Job{}).Where("id = ?", job.ID).
			Update("next_run_at", time.Now().Add(-time.Second)).Error; err != nil {
			t.Fatal(err)
		}
		got = f.dispatch(t, job.ID)
		result.AttemptID = got.AttemptID.String()
		result.RetryAfterSeconds = nil
		if err := f.worker.ProcessResult(t.Context(), result); err != nil {
			t.Fatal(err)
		}
	}
	got = f.get(t, job.ID)
	if got.Status != models.JobFailed || got.RetryCount != job.MaxRetries || got.OutputFile.Status != "FAILED" || got.LeaseExpiresAt != nil {
		t.Fatalf("retry limit failed to terminate: %+v", got)
	}
}
