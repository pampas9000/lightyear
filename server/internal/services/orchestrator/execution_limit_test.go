package orchestrator_test

import (
	"encoding/json"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/orchestrator"
	taskservice "transcoder/server/internal/services/task"
	taskqueue "transcoder/server/internal/task"

	"github.com/google/uuid"
)

// startedAgo makes the job's current attempt look like it began running d ago.
func (f fixture) startedAgo(t *testing.T, job models.Job, d time.Duration) time.Time {
	t.Helper()
	started := time.Now().Add(-d)
	if err := f.db.Model(&models.Attempt{}).Where("id = ?", *job.CurrentAttemptID).Update("started_at", started).Error; err != nil {
		t.Fatal(err)
	}
	return started
}

func (f fixture) beat(t *testing.T, job models.Job) {
	t.Helper()
	if err := f.svc.ProcessResult(t.Context(), result(job, taskqueue.ResultRunning)); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) reload(t *testing.T, id uuid.UUID) models.Job {
	t.Helper()
	var job models.Job
	if err := f.db.First(&job, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return job
}

func TestHeartbeatNeverExtendsLeasePastExecutionLimit(t *testing.T) {
	limit := time.Minute // shorter than LeaseDuration, so every beat is clamped
	f := newFixture(t, orchestrator.Limits{ExecutionLimit: limit})
	f.task = f.submit(t, uuid.New(), 1)
	f.dispatch(t)
	job := f.job(t, 0)
	f.beat(t, job)
	started := *f.attempt(t, job).StartedAt
	f.beat(t, job)
	if lease := *f.reload(t, job.ID).LeaseExpiresAt; !lease.Equal(started.Add(limit)) {
		t.Fatalf("lease %v should stop at the limit %v", lease, started.Add(limit))
	}
	// Nothing happens before the limit; the attempt is stopped right after it.
	if err := f.svc.Recover(t.Context(), started.Add(limit-time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := f.reload(t, job.ID); got.Status != models.JobProcessing {
		t.Fatalf("attempt expired before its limit: %+v", got)
	}
	if err := f.svc.Recover(t.Context(), started.Add(limit+time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := f.reload(t, job.ID); got.Status != models.JobPending || got.ErrorCode != "execution_timeout" {
		t.Fatalf("attempt not stopped at its limit: %+v", got)
	}
}

func TestAttemptPastExecutionLimitIsExpiredAndRetried(t *testing.T) {
	f := newFixture(t, orchestrator.Limits{ExecutionLimit: 5 * time.Minute})
	f.task = f.submit(t, uuid.New(), 1)
	f.dispatch(t)
	job := f.job(t, 0)
	f.beat(t, job)
	// The worker is still alive and beating, but has run past the limit.
	f.startedAgo(t, job, 6*time.Minute)
	f.beat(t, job)
	if lease := *f.reload(t, job.ID).LeaseExpiresAt; lease.After(time.Now()) {
		t.Fatalf("a beat past the limit renewed the lease to %v", lease)
	}
	if err := f.svc.Recover(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	got := f.reload(t, job.ID)
	old := f.attempt(t, job)
	if got.Status != models.JobPending || got.ErrorCode != "execution_timeout" ||
		got.ErrorMessage != "attempt exceeded the execution time limit" || got.NextRunAt == nil ||
		old.Status != models.AttemptExpired {
		t.Fatalf("over-limit attempt not expired and retried: job=%+v attempt=%+v", got, old)
	}
	if len(f.queue.cancelled) != 1 || f.queue.cancelled[0] != old.ID.String() {
		t.Fatalf("over-limit attempt not signalled: %v", f.queue.cancelled)
	}
	// The retry is a new attempt with its own clock.
	if _, _, err := f.svc.DispatchDue(t.Context(), *got.NextRunAt); err != nil {
		t.Fatal(err)
	}
	retried := f.reload(t, job.ID)
	if retried.Status != models.JobProcessing || retried.Attempts != 2 || *retried.CurrentAttemptID == old.ID {
		t.Fatalf("job not re-dispatched: %+v", retried)
	}
}

func TestTargetJobUsesItsOwnExecutionLimit(t *testing.T) {
	f := newFixture(t, orchestrator.Limits{ExecutionLimit: time.Minute, TargetExecutionLimit: 5 * time.Minute})
	owner := uuid.New()
	file := models.File{OwnerID: owner, Status: "UPLOADED", Name: "photo.png", Path: "uploads/" + uuid.NewString() + ".png"}
	if err := f.db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	task, err := f.tasks.CreateTask(t.Context(), taskservice.CreateTaskInput{
		OwnerID: owner, Items: []taskservice.TaskItemInput{{FileID: file.ID}},
		Outputs: json.RawMessage(`[{"options":{"format":"avif"},"target":{"max_bytes":50000}},{"options":{"format":"webp"}}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.task = task
	f.dispatch(t)
	search, plain := f.job(t, 0), f.job(t, 1)
	if search.Target == nil || plain.Target != nil {
		t.Fatalf("unexpected outputs: %+v / %+v", search.Target, plain.Target)
	}
	for _, job := range []models.Job{search, plain} {
		f.beat(t, job)
		f.startedAgo(t, job, 4*time.Minute)
		f.beat(t, job)
	}
	if err := f.svc.Recover(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := f.reload(t, plain.ID); got.Status != models.JobPending || got.ErrorCode != "execution_timeout" {
		t.Fatalf("plain job should hit the 1m limit: %+v", got)
	}
	got := f.reload(t, search.ID)
	if got.Status != models.JobProcessing {
		t.Fatalf("search job stopped before its 5m limit: %+v", got)
	}
	if limit := f.attempt(t, search).StartedAt.Add(5 * time.Minute); got.LeaseExpiresAt.After(limit) {
		t.Fatalf("search lease %v passes its limit %v", got.LeaseExpiresAt, limit)
	}
	if err := f.svc.Recover(t.Context(), time.Now().Add(time.Minute+time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := f.reload(t, search.ID); got.Status != models.JobPending ||
		got.ErrorMessage != "attempt exceeded the execution time limit" {
		t.Fatalf("search job not stopped at its own limit: %+v", got)
	}
}
