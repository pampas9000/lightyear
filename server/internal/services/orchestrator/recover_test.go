package orchestrator_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/orchestrator"
)

// recoverAfterStart runs Recover once the dispatched attempt's StartTimeout
// has passed and returns the job's lease afterwards.
func (f fixture) recoverAfterStart(t *testing.T) time.Time {
	t.Helper()
	at := time.Now().Add(orchestrator.StartTimeout + time.Second)
	if err := f.svc.Recover(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	job := f.job(t, 0)
	if job.Status != models.JobProcessing || job.Attempts != 1 || job.LeaseExpiresAt == nil {
		t.Fatalf("job should stay on its first attempt: %+v", job)
	}
	if !job.LeaseExpiresAt.After(at) {
		t.Fatalf("lease not renewed: %v", job.LeaseExpiresAt)
	}
	return *job.LeaseExpiresAt
}

// A backlog is not a loss: while the dispatch still waits in the stream,
// recovery renews the lease and adds no duplicate.
func TestQueuedDispatchStillInTheStreamIsNotResent(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	f.recoverAfterStart(t)
	if len(f.queue.sent) != 1 || len(f.outbox(t, job)) != 1 || f.queue.checks != 1 {
		t.Fatalf("expected no re-send: sent=%d outbox=%d checks=%d", len(f.queue.sent), len(f.outbox(t, job)), f.queue.checks)
	}
	if msgs := f.outbox(t, job); msgs[0].StreamID != fmt.Sprintf("%d-1", fakeStreamMS) {
		t.Fatalf("send did not record its stream id: %q", msgs[0].StreamID)
	}
}

func TestDeliveredDispatchThatNeverStartedIsResent(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	f.queue.deliverAll()
	f.recoverAfterStart(t)
	if len(f.queue.sent) != 2 || f.queue.sent[1].AttemptID != job.CurrentAttemptID.String() {
		t.Fatalf("expected the attempt re-sent: %+v", f.queue.sent)
	}
	// The check looks at the newest send: once the re-send waits in the
	// stream, the next round leaves it alone.
	if err := f.svc.Recover(t.Context(), time.Now().Add(2*orchestrator.StartTimeout+2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.sent) != 2 {
		t.Fatalf("waiting re-send was duplicated: sent=%d", len(f.queue.sent))
	}
}

// The entry was trimmed (MAXLEN) or lost with Redis: send it again.
func TestDispatchMissingFromTheStreamIsResent(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	clear(f.queue.entries)
	f.recoverAfterStart(t)
	if len(f.queue.sent) != 2 {
		t.Fatalf("expected a re-send: sent=%d", len(f.queue.sent))
	}
}

// A send without a recorded stream ID (before the column existed) cannot be
// checked, so it is re-sent as before.
func TestDispatchWithoutStreamIDIsResent(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	f.db.Model(&models.OutboxMessage{}).Where("attempt_id = ?", *job.CurrentAttemptID).Update("stream_id", "")
	f.recoverAfterStart(t)
	if len(f.queue.sent) != 2 || f.queue.checks != 0 {
		t.Fatalf("expected a re-send without a check: sent=%d checks=%d", len(f.queue.sent), f.queue.checks)
	}
}

// If Redis cannot say, recovery renews the lease and sends nothing: a send
// would not get through either, and the next round checks again.
func TestRedisErrorRenewsWithoutResending(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	f.queue.deliverAll()
	f.queue.waitErr = errors.New("redis down")
	f.recoverAfterStart(t)
	if len(f.queue.sent) != 1 || len(f.outbox(t, job)) != 1 {
		t.Fatalf("expected no re-send while Redis errs: sent=%d outbox=%d", len(f.queue.sent), len(f.outbox(t, job)))
	}
	f.queue.waitErr = nil
	if err := f.svc.Recover(t.Context(), time.Now().Add(2*orchestrator.StartTimeout+2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.sent) != 2 {
		t.Fatalf("expected a re-send once Redis answers: sent=%d", len(f.queue.sent))
	}
}

// Stream IDs compare as numbers: <ms>-10 comes after <ms>-2.
func TestWaitingComparesStreamIDsAsNumbers(t *testing.T) {
	f := setup(t, 1)
	f.queue.seq = 9 // the dispatch gets <ms>-10
	f.dispatch(t)
	f.queue.lastDelivered = fmt.Sprintf("%d-2", fakeStreamMS)
	f.recoverAfterStart(t)
	if len(f.queue.sent) != 1 {
		t.Fatalf("<ms>-10 is after <ms>-2 and still waiting: sent=%d", len(f.queue.sent))
	}
}

// A RUNNING attempt's expiry needs no stream check.
func TestRunningAttemptIsNotCheckedInTheStream(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	if err := f.svc.ProcessResult(t.Context(), result(job, "RUNNING")); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Recover(t.Context(), time.Now().Add(orchestrator.LeaseDuration+time.Second)); err != nil {
		t.Fatal(err)
	}
	if f.queue.checks != 0 || f.job(t, 0).Status != models.JobPending {
		t.Fatalf("expected the running attempt abandoned without a check: checks=%d", f.queue.checks)
	}
}

// Once an attempt completes, its other copies must not run or upload over
// the published output: the attempt is marked cancelled.
func TestCompletionCancelsTheAttemptsOtherCopies(t *testing.T) {
	f := setup(t, 2)
	f.dispatch(t)
	job := f.job(t, 0)
	other := f.job(t, 1)
	a := f.attempt(t, job)
	if err := f.svc.ProcessResult(t.Context(), result(job, "RUNNING")); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.cancelled) != 0 {
		t.Fatalf("a heartbeat cancelled something: %v", f.queue.cancelled)
	}
	if err := f.svc.ProcessResult(t.Context(), failed(other, true)); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.cancelled) != 0 {
		t.Fatalf("a failure is not a completion: %v", f.queue.cancelled)
	}
	if err := f.svc.ProcessResult(t.Context(), completed(job, "outputs/elsewhere.avif")); err == nil {
		t.Fatal("expected the invalid completion rejected")
	}
	if len(f.queue.cancelled) != 0 {
		t.Fatalf("a rejected completion cancelled the attempt: %v", f.queue.cancelled)
	}
	if err := f.svc.ProcessResult(t.Context(), completed(job, a.OutputKey)); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.cancelled) != 1 || f.queue.cancelled[0] != a.ID.String() {
		t.Fatalf("completed attempt not cancelled: %v", f.queue.cancelled)
	}
	// The duplicate result is ignored and signals nothing more.
	if err := f.svc.ProcessResult(t.Context(), completed(job, a.OutputKey)); err != nil {
		t.Fatal(err)
	}
	if len(f.queue.cancelled) != 1 {
		t.Fatalf("duplicate completion signalled again: %v", f.queue.cancelled)
	}
}
