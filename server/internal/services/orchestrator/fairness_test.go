package orchestrator_test

import (
	"sync"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/orchestrator"

	"github.com/google/uuid"
)

func (f fixture) count(t *testing.T, owner uuid.UUID, status models.JobStatus) int64 {
	t.Helper()
	var n int64
	if err := f.db.Model(&models.Job{}).Where("owner_id = ? AND status = ?", owner, status).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func (f fixture) jobsOf(t *testing.T, task *models.Task) []models.Job {
	t.Helper()
	got, err := f.tasks.GetTask(t.Context(), task.OwnerID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got.Jobs
}

func TestLargeBacklogDoesNotDelayAnotherUser(t *testing.T) {
	f := newFixture(t, orchestrator.Limits{MaxProcessingPerOwner: 100})
	big, small := uuid.New(), uuid.New()
	f.submit(t, big, 30) // submitted first, so every job is due earlier
	smallTask := f.submit(t, small, 1)

	sent, _, err := f.svc.DispatchDue(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sent != 20 {
		t.Fatalf("expected a full batch of 20, sent %d", sent)
	}
	if job := f.jobsOf(t, smallTask)[0]; job.Status != models.JobProcessing {
		t.Fatalf("small user's job waited behind the backlog: %s", job.Status)
	}
	if n := f.count(t, big, models.JobProcessing); n != 19 {
		t.Fatalf("big user should fill the rest of the batch, got %d", n)
	}
}

func TestOwnerAtCapIsSkippedUntilAJobFinishes(t *testing.T) {
	f := newFixture(t, orchestrator.Limits{MaxProcessingPerOwner: 3})
	capped, other := uuid.New(), uuid.New()
	cappedTask := f.submit(t, capped, 6)
	f.submit(t, other, 2)

	if sent, _, err := f.svc.DispatchDue(t.Context(), time.Now()); err != nil || sent != 5 {
		t.Fatalf("first round: sent %d, err %v", sent, err)
	}
	if n := f.count(t, capped, models.JobProcessing); n != 3 {
		t.Fatalf("capped user has %d processing, want 3", n)
	}
	if n := f.count(t, other, models.JobProcessing); n != 2 {
		t.Fatalf("other user has %d processing, want 2", n)
	}
	if sent, _, err := f.svc.DispatchDue(t.Context(), time.Now()); err != nil || sent != 0 {
		t.Fatalf("user at cap must be skipped: sent %d, err %v", sent, err)
	}

	for _, job := range f.jobsOf(t, cappedTask) {
		if job.Status == models.JobProcessing {
			if err := f.svc.ProcessResult(t.Context(), completed(job, f.attempt(t, job).OutputKey)); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if sent, _, err := f.svc.DispatchDue(t.Context(), time.Now()); err != nil || sent != 1 {
		t.Fatalf("a finished job frees exactly one slot: sent %d, err %v", sent, err)
	}
	if n := f.count(t, capped, models.JobProcessing); n != 3 {
		t.Fatalf("capped user has %d processing after refill, want 3", n)
	}
}

func TestConcurrentOrchestratorsClaimOnceAndRespectTheCap(t *testing.T) {
	const owners, perOwner, limit = 5, 10, 4
	f := newFixture(t, orchestrator.Limits{MaxProcessingPerOwner: limit})
	ids := make([]uuid.UUID, owners)
	for i := range ids {
		ids[i] = uuid.New()
		f.submit(t, ids[i], perOwner)
	}
	queues := []*fakeQueue{{}, {}}
	var wg sync.WaitGroup
	for _, q := range queues {
		svc := orchestrator.NewService(f.db, q, orchestrator.Limits{MaxProcessingPerOwner: limit}, lifecycle.Policy{})
		wg.Go(func() {
			for range 3 {
				if _, _, err := svc.DispatchDue(t.Context(), time.Now()); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, q := range queues {
		for _, d := range q.sent {
			if seen[d.JobID] {
				t.Fatalf("job %s dispatched twice", d.JobID)
			}
			seen[d.JobID] = true
		}
	}
	var attempts int64
	f.db.Model(&models.Attempt{}).Count(&attempts)
	if len(seen) != owners*limit || attempts != owners*limit {
		t.Fatalf("expected %d dispatches and attempts, got %d and %d", owners*limit, len(seen), attempts)
	}
	for _, owner := range ids {
		if n := f.count(t, owner, models.JobProcessing); n != limit {
			t.Fatalf("owner %s has %d processing, want %d", owner, n, limit)
		}
	}
}
