package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/orchestrator"
	taskservice "transcoder/server/internal/services/task"
	taskqueue "transcoder/server/internal/task"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type fakeQueue struct {
	mu        sync.Mutex
	failSends int
	sent      []taskqueue.Dispatch
	cancelled []string
}

func (q *fakeQueue) Send(_ context.Context, _ string, payload []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.failSends > 0 {
		q.failSends--
		return errors.New("redis down")
	}
	var d taskqueue.Dispatch
	if err := json.Unmarshal(payload, &d); err != nil {
		return err
	}
	q.sent = append(q.sent, d)
	return nil
}

func (q *fakeQueue) Cancel(_ context.Context, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cancelled = append(q.cancelled, id)
	return nil
}

type fixture struct {
	db    *gorm.DB
	queue *fakeQueue
	svc   *orchestrator.Service
	tasks *taskservice.Service
	task  *models.Task
}

func setup(t *testing.T, count int) fixture {
	t.Helper()
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	var items []taskservice.TaskItemInput
	for range count {
		file := models.File{OwnerID: owner, Status: "UPLOADED", Name: "photo.png", Path: "uploads/" + uuid.NewString() + ".png"}
		if err := db.Create(&file).Error; err != nil {
			t.Fatal(err)
		}
		items = append(items, taskservice.TaskItemInput{FileID: file.ID})
	}
	tasks := taskservice.NewService(db, nil)
	parent, err := tasks.CreateTask(t.Context(), taskservice.CreateTaskInput{
		OwnerID: owner, Items: items, Options: json.RawMessage(`{"format":"avif"}`)})
	if err != nil {
		t.Fatal(err)
	}
	queue := &fakeQueue{}
	return fixture{db, queue, orchestrator.NewService(db, queue), tasks, parent}
}

func (f fixture) job(t *testing.T, i int) models.Job {
	t.Helper()
	got, err := f.tasks.GetTask(t.Context(), f.task.OwnerID, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got.Jobs[i]
}

func (f fixture) attempt(t *testing.T, job models.Job) models.Attempt {
	t.Helper()
	var a models.Attempt
	if err := f.db.First(&a, "id = ?", *job.CurrentAttemptID).Error; err != nil {
		t.Fatal(err)
	}
	return a
}

func (f fixture) dispatch(t *testing.T) {
	t.Helper()
	if _, err := f.svc.DispatchDue(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func result(job models.Job, status taskqueue.ResultStatus) taskqueue.Result {
	return taskqueue.Result{V: taskqueue.Version, JobID: job.ID.String(), AttemptID: job.CurrentAttemptID.String(), Status: status}
}

func completed(job models.Job, key string) taskqueue.Result {
	r := result(job, taskqueue.ResultCompleted)
	r.Artifact = &taskqueue.Artifact{Key: key, Size: 1234, Mime: "image/avif", Width: 640, Height: 480}
	return r
}

func failed(job models.Job, retryable bool) taskqueue.Result {
	r := result(job, taskqueue.ResultFailed)
	r.Error = &taskqueue.ResultError{Code: "processing_failed", Message: "boom", Retryable: retryable}
	return r
}

func TestConcurrentDispatchClaimsEachJobOnce(t *testing.T) {
	f := setup(t, 5)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { _, _ = f.svc.DispatchDue(t.Context(), time.Now()) })
	}
	wg.Wait()
	var attempts int64
	f.db.Model(&models.Attempt{}).Count(&attempts)
	if attempts != 5 || len(f.queue.sent) != 5 {
		t.Fatalf("expected 5 attempts and sends, got %d and %d", attempts, len(f.queue.sent))
	}
	job := f.job(t, 0)
	if job.Status != models.JobProcessing || job.Attempts != 1 {
		t.Fatalf("job not claimed: %+v", job)
	}
	var sent taskqueue.Dispatch
	for _, d := range f.queue.sent {
		if d.JobID == job.ID.String() {
			sent = d
		}
	}
	if sent.Op != models.OpImageConvert || sent.Options.Format != "avif" || sent.SourceKey != job.SourceFile.Path ||
		sent.OutputKey != f.attempt(t, job).OutputKey {
		t.Fatalf("unexpected dispatch payload: %+v", sent)
	}
}

func TestCompletionPublishesOutputForTheCurrentAttemptOnly(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	key := f.attempt(t, job).OutputKey

	if err := f.svc.ProcessResult(t.Context(), completed(job, "outputs/elsewhere.avif")); !errors.Is(err, orchestrator.ErrInvalidResult) {
		t.Fatalf("artifact outside the attempt must be rejected, got %v", err)
	}
	stale := completed(job, key)
	stale.AttemptID = uuid.NewString()
	if err := f.svc.ProcessResult(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	if f.job(t, 0).Status != models.JobProcessing {
		t.Fatal("a foreign attempt changed the job")
	}

	if err := f.svc.ProcessResult(t.Context(), completed(job, key)); err != nil {
		t.Fatal(err)
	}
	done := f.job(t, 0)
	if done.Status != models.JobCompleted || done.OutputFile == nil || done.OutputFile.Path != key ||
		done.OutputFile.Name != "photo.avif" || done.OutputFile.Status != "UPLOADED" {
		t.Fatalf("output not published: %+v", done)
	}
	// A duplicate delivery of the same result is a no-op.
	if err := f.svc.ProcessResult(t.Context(), completed(job, key)); err != nil {
		t.Fatal(err)
	}
	var files int64
	f.db.Model(&models.File{}).Where("path = ?", key).Count(&files)
	if files != 1 {
		t.Fatalf("duplicate result created %d output files", files)
	}
	var task models.Task
	f.db.First(&task, "id = ?", f.task.ID)
	if task.Status != models.TaskCompleted {
		t.Fatalf("task status %s", task.Status)
	}
}

func TestHeartbeatRenewsLeaseAndNeverLowersProgress(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	beat := result(job, taskqueue.ResultRunning)
	beat.Progress = new(40)
	if err := f.svc.ProcessResult(t.Context(), beat); err != nil {
		t.Fatal(err)
	}
	beat.Progress = new(10)
	if err := f.svc.ProcessResult(t.Context(), beat); err != nil {
		t.Fatal(err)
	}
	got := f.job(t, 0)
	if got.Progress != 40 || got.LeaseExpiresAt.After(time.Now().Add(orchestrator.LeaseDuration)) {
		t.Fatalf("unexpected lease or progress: %+v", got)
	}
	if a := f.attempt(t, got); a.Status != models.AttemptRunning || a.StartedAt == nil {
		t.Fatalf("attempt not running: %+v", a)
	}
}

func TestRetryableFailureBacksOffUntilAttemptsRunOut(t *testing.T) {
	f := setup(t, 1)
	for attempt := 1; ; attempt++ {
		if _, err := f.svc.DispatchDue(t.Context(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		job := f.job(t, 0)
		if err := f.svc.ProcessResult(t.Context(), failed(job, true)); err != nil {
			t.Fatal(err)
		}
		got := f.job(t, 0)
		if attempt < got.MaxAttempts {
			if got.Status != models.JobPending || got.NextRunAt == nil || !got.NextRunAt.After(time.Now()) {
				t.Fatalf("attempt %d should back off: %+v", attempt, got)
			}
			continue
		}
		if got.Status != models.JobFailed || got.ErrorCode != "processing_failed" || got.Attempts != got.MaxAttempts {
			t.Fatalf("job should fail after %d attempts: %+v", attempt, got)
		}
		return
	}
}

func TestNonRetryableFailureFailsImmediately(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	if err := f.svc.ProcessResult(t.Context(), failed(f.job(t, 0), false)); err != nil {
		t.Fatal(err)
	}
	if got := f.job(t, 0); got.Status != models.JobFailed || got.Attempts != 1 {
		t.Fatalf("expected immediate failure: %+v", got)
	}
}

func TestUnsentAttemptIsSentByRecovery(t *testing.T) {
	f := setup(t, 1)
	f.queue.failSends = 1
	f.dispatch(t)
	job := f.job(t, 0)
	if len(f.queue.sent) != 0 || f.attempt(t, job).DispatchedAt != nil {
		t.Fatal("failed send must leave the attempt unsent")
	}
	if err := f.svc.Recover(t.Context(), time.Now().Add(orchestrator.UnsentGrace+time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.sent) != 1 || f.queue.sent[0].AttemptID != job.CurrentAttemptID.String() || f.attempt(t, job).DispatchedAt == nil {
		t.Fatalf("recovery did not send the stored attempt: %+v", f.queue.sent)
	}
}

func TestUnstartedAttemptIsResentWithoutConsumingARetry(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	if err := f.svc.Recover(t.Context(), time.Now().Add(orchestrator.StartTimeout+time.Second)); err != nil {
		t.Fatal(err)
	}
	got := f.job(t, 0)
	if len(f.queue.sent) != 2 || f.queue.sent[1].AttemptID != job.CurrentAttemptID.String() ||
		got.Attempts != 1 || *got.CurrentAttemptID != *job.CurrentAttemptID || got.Status != models.JobProcessing {
		t.Fatalf("expected the same attempt re-sent: sent=%d job=%+v", len(f.queue.sent), got)
	}
}

func TestDeadRunningAttemptIsAbandonedAndRetried(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	if err := f.svc.ProcessResult(t.Context(), result(job, taskqueue.ResultRunning)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Recover(t.Context(), time.Now().Add(orchestrator.LeaseDuration+time.Second)); err != nil {
		t.Fatal(err)
	}
	got := f.job(t, 0)
	var old models.Attempt
	f.db.First(&old, "id = ?", *job.CurrentAttemptID)
	if got.Status != models.JobPending || got.ErrorCode != "execution_timeout" || old.Status != models.AttemptExpired {
		t.Fatalf("dead attempt not abandoned: job=%+v attempt=%+v", got, old)
	}
	if len(f.queue.cancelled) != 1 || f.queue.cancelled[0] != old.ID.String() {
		t.Fatalf("abandoned attempt not signalled: %v", f.queue.cancelled)
	}
	// Its late completion must not publish anything.
	if err := f.svc.ProcessResult(t.Context(), completed(job, old.OutputKey)); err != nil {
		t.Fatal(err)
	}
	if f.job(t, 0).Status != models.JobPending {
		t.Fatal("late result from an expired attempt changed the job")
	}
}

func TestCancelledJobIgnoresLateCompletion(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	if _, err := f.tasks.CancelTask(t.Context(), f.task.OwnerID, f.task.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ProcessResult(t.Context(), completed(job, f.attempt(t, job).OutputKey)); err != nil {
		t.Fatal(err)
	}
	if got := f.job(t, 0); got.Status != models.JobCancelled || got.OutputFileID != nil {
		t.Fatalf("cancelled job was completed: %+v", got)
	}
}

func TestMissingSourceFailsTheJob(t *testing.T) {
	f := setup(t, 1)
	f.db.Model(&models.File{}).Where("id = ?", f.job(t, 0).SourceFileID).Update("status", "DELETING")
	f.dispatch(t)
	if got := f.job(t, 0); got.Status != models.JobFailed || got.ErrorCode != "source_unavailable" {
		t.Fatalf("expected source_unavailable: %+v", got)
	}
}

func TestParallelSiblingCompletionAggregatesTask(t *testing.T) {
	f := setup(t, 4)
	f.dispatch(t)
	got, _ := f.tasks.GetTask(t.Context(), f.task.OwnerID, f.task.ID)
	var wg sync.WaitGroup
	for _, job := range got.Jobs {
		key := f.attempt(t, job).OutputKey
		wg.Go(func() {
			if err := f.svc.ProcessResult(t.Context(), completed(job, key)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var task models.Task
	f.db.First(&task, "id = ?", f.task.ID)
	if task.Status != models.TaskCompleted {
		t.Fatalf("task status %s", task.Status)
	}
}
