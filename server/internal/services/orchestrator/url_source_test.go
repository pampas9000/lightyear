package orchestrator_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/orchestrator"
	taskservice "transcoder/server/internal/services/task"
	taskqueue "transcoder/server/internal/task"

	"github.com/google/uuid"
)

const sourceURL = "https://images.example.com/photos/cat.jpg?X-Amz-Signature=abc"

// submitURL creates a task with one URL item and the given outputs.
func (f *fixture) submitURL(t *testing.T, owner uuid.UUID, outputs string) {
	t.Helper()
	task, err := f.tasks.CreateTask(t.Context(), taskservice.CreateTaskInput{
		OwnerID: owner, Items: []taskservice.TaskItemInput{{URL: sourceURL}}, Outputs: json.RawMessage(outputs)})
	if err != nil {
		t.Fatal(err)
	}
	f.task = task
}

func (f fixture) source(t *testing.T) models.File {
	t.Helper()
	var file models.File
	if err := f.db.First(&file, "id = ?", f.job(t, 0).SourceFileID).Error; err != nil {
		t.Fatal(err)
	}
	return file
}

func (f fixture) process(t *testing.T, r taskqueue.Result) {
	t.Helper()
	if err := f.svc.ProcessResult(t.Context(), r); err != nil {
		t.Fatal(err)
	}
}

func fetchedReport(job models.Job, file models.File) taskqueue.Result {
	r := result(job, taskqueue.ResultRunning)
	r.Source = &taskqueue.FetchedSource{Key: file.Path, Size: 4321, Mime: "image/jpeg"}
	return r
}

func TestURLSourceIsFetchedOnceAndNotAgainOnRetry(t *testing.T) {
	f := newFixture(t, orchestrator.Limits{})
	f.submitURL(t, uuid.New(), `[{"options":{"format":"avif"}}]`)
	file := f.source(t)
	if file.Status != models.FileFetching || file.SourceURL != sourceURL || file.Name != "cat.jpg" ||
		!strings.HasPrefix(file.Path, "uploads/"+f.task.OwnerID.String()+"/") || !strings.HasSuffix(file.Path, ".jpg") {
		t.Fatalf("unexpected URL file: %+v", file)
	}

	f.dispatch(t)
	if len(f.queue.sent) != 1 || f.queue.sent[0].SourceURL != sourceURL || f.queue.sent[0].SourceKey != file.Path {
		t.Fatalf("first dispatch must carry the URL: %+v", f.queue.sent)
	}
	job := f.job(t, 0)
	// The lifecycle sweep may have scheduled the FETCHING file from its
	// creation; the fetch restarts retention from uploaded_at.
	stale := time.Now().Add(-time.Hour)
	if err := f.db.Model(&models.File{}).Where("id = ?", file.ID).Update("expires_at", stale).Error; err != nil {
		t.Fatal(err)
	}
	f.process(t, fetchedReport(job, file))
	file = f.source(t)
	if file.Status != models.FileUploaded || file.Size != 4321 || file.MimeType != "image/jpeg" ||
		file.UploadedAt == nil || file.ExpiresAt == nil || !file.ExpiresAt.After(*file.UploadedAt) {
		t.Fatalf("fetched file must be UPLOADED with its size, type and a retention from uploaded_at: %+v", file)
	}
	// A repeated report changes nothing.
	f.process(t, fetchedReport(job, file))

	r := failed(job, true)
	r.Error.Code = "storage_failed"
	f.process(t, r)
	if got := f.job(t, 0); got.Status != models.JobPending {
		t.Fatalf("retryable failure must requeue, got %s", got.Status)
	}
	if _, err := f.svc.DispatchDue(t.Context(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.sent) != 2 || f.queue.sent[1].SourceURL != "" || f.queue.sent[1].SourceKey != file.Path {
		t.Fatalf("a retry must read the stored source, not fetch again: %+v", f.queue.sent)
	}
}

func TestFetchReportedWithTheFailureStillCounts(t *testing.T) {
	f := newFixture(t, orchestrator.Limits{})
	f.submitURL(t, uuid.New(), `[{"options":{"format":"avif"}}]`)
	f.dispatch(t)
	file, job := f.source(t), f.job(t, 0)
	r := failed(job, false)
	r.Error.Code = "unsupported_input"
	r.Source = &taskqueue.FetchedSource{Key: file.Path, Size: 99, Mime: "application/octet-stream"}
	f.process(t, r)
	if got := f.source(t); got.Status != models.FileUploaded || got.Size != 99 {
		t.Fatalf("the fetch must be recorded even when processing fails: %+v", got)
	}
	if got := f.job(t, 0); got.Status != models.JobFailed || got.ErrorCode != "unsupported_input" {
		t.Fatalf("job must fail: %s %s", got.Status, got.ErrorCode)
	}
}

func TestFailedFetchIsTriedAgainOnRetry(t *testing.T) {
	f := newFixture(t, orchestrator.Limits{})
	f.submitURL(t, uuid.New(), `[{"options":{"format":"avif"}}]`)
	f.dispatch(t)
	r := failed(f.job(t, 0), true)
	r.Error.Code = "fetch_failed"
	f.process(t, r)
	if got := f.source(t); got.Status != models.FileFetching {
		t.Fatalf("an unfetched source stays FETCHING, got %s", got.Status)
	}
	if _, err := f.svc.DispatchDue(t.Context(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.sent) != 2 || f.queue.sent[1].SourceURL != sourceURL {
		t.Fatalf("the retry must fetch again: %+v", f.queue.sent)
	}

	// A job that failed for good can be retried by hand while still FETCHING.
	r = failed(f.job(t, 0), false)
	r.Error.Code = "url_rejected"
	f.process(t, r)
	if _, err := f.tasks.RetryTask(t.Context(), taskservice.RetryTaskInput{
		OwnerID: f.task.OwnerID, TaskID: f.task.ID, RetryAll: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.DispatchDue(t.Context(), time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.sent) != 3 || f.queue.sent[2].SourceURL != sourceURL {
		t.Fatalf("a manual retry must fetch again: %+v", f.queue.sent)
	}
}

func TestOutputsOfOneURLFetchItOnce(t *testing.T) {
	f := newFixture(t, orchestrator.Limits{})
	f.submitURL(t, uuid.New(), `[{"options":{"format":"avif"}},{"options":{"format":"webp"}}]`)
	now := time.Now()
	if _, err := f.svc.DispatchDue(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.sent) != 1 {
		t.Fatalf("only one job may fetch the URL, sent %d", len(f.queue.sent))
	}
	fetching := f.job(t, 0)
	waiting := f.job(t, 1)
	if fetching.Status != models.JobProcessing {
		fetching, waiting = waiting, fetching
	}
	if waiting.Status != models.JobPending || waiting.NextRunAt == nil || !waiting.NextRunAt.After(now) {
		t.Fatalf("the other job must wait: %+v", waiting)
	}
	// Still fetching: the waiting job is put off again.
	if _, err := f.svc.DispatchDue(t.Context(), now.Add(orchestrator.FetchWait)); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.sent) != 1 {
		t.Fatalf("still only one dispatch, got %d", len(f.queue.sent))
	}
	f.process(t, fetchedReport(fetching, f.source(t)))
	if _, err := f.svc.DispatchDue(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.sent) != 2 || f.queue.sent[1].SourceURL != "" || f.queue.sent[1].JobID != waiting.ID.String() {
		t.Fatalf("the other job must read the stored source: %+v", f.queue.sent)
	}
}

func TestFetchReportOutsideTheSourceIsRejected(t *testing.T) {
	f := newFixture(t, orchestrator.Limits{})
	f.submitURL(t, uuid.New(), `[{"options":{"format":"avif"}}]`)
	f.dispatch(t)
	job, file := f.job(t, 0), f.source(t)
	for _, src := range []taskqueue.FetchedSource{
		{Key: "uploads/someone-else/x.jpg", Size: 10, Mime: "image/jpeg"},
		{Key: file.Path, Size: 0, Mime: "image/jpeg"},
		{Key: file.Path, Size: 10, Mime: ""},
	} {
		r := result(job, taskqueue.ResultRunning)
		r.Source = &src
		if err := f.svc.ProcessResult(t.Context(), r); !errors.Is(err, orchestrator.ErrInvalidResult) {
			t.Fatalf("%+v: want ErrInvalidResult, got %v", src, err)
		}
	}
	if got := f.source(t); got.Status != models.FileFetching {
		t.Fatalf("file must stay FETCHING, got %s", got.Status)
	}
}

func TestURLSourcesNeverReuseOutputs(t *testing.T) {
	f := newFixture(t, orchestrator.Limits{})
	owner := uuid.New()
	f.submitURL(t, owner, `[{"options":{"format":"avif"}}]`)
	f.dispatch(t)
	job := f.job(t, 0)
	f.process(t, fetchedReport(job, f.source(t)))
	f.process(t, completed(job, f.attempt(t, job).OutputKey))
	if got := f.job(t, 0); got.Status != models.JobCompleted {
		t.Fatalf("first task must complete, got %s", got.Status)
	}
	first := f.source(t)

	// The same URL with the same options is fetched and processed again.
	f.submitURL(t, owner, `[{"options":{"format":"avif"}}]`)
	second := f.job(t, 0)
	if second.Status != models.JobPending || second.OutputFileID != nil {
		t.Fatalf("a new URL task must not reuse an output: %+v", second)
	}
	if f.source(t).ID == first.ID || f.source(t).Status != models.FileFetching {
		t.Fatal("each URL task must get its own FETCHING source file")
	}
}
