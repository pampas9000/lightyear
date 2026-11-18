package orchestrator_test

import (
	"testing"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/orchestrator"

	"github.com/google/uuid"
)

func (f fixture) usage(t *testing.T) []models.UsageRecord {
	t.Helper()
	var recs []models.UsageRecord
	if err := f.db.Order("created_at, kind").Find(&recs).Error; err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestCompletedAttemptIsMeteredOnceByEncodeRounds(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	r := completed(job, f.attempt(t, job).OutputKey)
	rounds, ms := 6, int64(820)
	r.EncodeRounds, r.ComputeMS = &rounds, &ms
	f.process(t, r)
	// A redelivered result is ignored, and a record for the same attempt
	// would be refused by the (attempt_id, kind) key anyway.
	f.process(t, r)

	recs := f.usage(t)
	if len(recs) != 1 {
		t.Fatalf("want one usage record, got %+v", recs)
	}
	got := recs[0]
	if got.Kind != models.UsageProcess || got.Units != 6 || got.EncodeRounds != 6 || got.ComputeMS != 820 ||
		got.OwnerID != f.task.OwnerID || got.TaskID != f.task.ID || got.JobID != job.ID ||
		got.AttemptID == nil || *got.AttemptID != *job.CurrentAttemptID ||
		got.OutputBytes != 1234 || got.OutputPixels != 640*480 {
		t.Fatalf("unexpected record: %+v", got)
	}
}

func TestCompletionWithoutReportedRoundsCostsOneUnit(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	f.process(t, completed(job, f.attempt(t, job).OutputKey))
	if recs := f.usage(t); len(recs) != 1 || recs[0].Units != 1 {
		t.Fatalf("want 1 unit, got %+v", recs)
	}
}

func TestFailedAttemptsAreRecordedWithoutUnits(t *testing.T) {
	f := setup(t, 1)
	f.dispatch(t)
	job := f.job(t, 0)
	r := failed(job, false)
	ms := int64(300)
	r.ComputeMS = &ms
	f.process(t, r)
	recs := f.usage(t)
	if len(recs) != 1 || recs[0].Kind != models.UsageFailed || recs[0].Units != 0 || recs[0].ComputeMS != 300 {
		t.Fatalf("unexpected records: %+v", recs)
	}
}

func TestURLFetchIsRecordedOnce(t *testing.T) {
	f := newFixture(t, orchestrator.Limits{})
	f.submitURL(t, uuid.New(), `[{"options":{"format":"avif"}}]`)
	f.dispatch(t)
	file, job := f.source(t), f.job(t, 0)
	f.process(t, fetchedReport(job, file))
	f.process(t, fetchedReport(job, file))
	f.process(t, completed(job, f.attempt(t, job).OutputKey))

	var fetches, processes []models.UsageRecord
	for _, r := range f.usage(t) {
		switch r.Kind {
		case models.UsageFetch:
			fetches = append(fetches, r)
		case models.UsageProcess:
			processes = append(processes, r)
		}
	}
	if len(fetches) != 1 || fetches[0].InputBytes != 4321 || fetches[0].Units != 0 {
		t.Fatalf("want one fetch of 4321 bytes, got %+v", fetches)
	}
	if len(processes) != 1 || processes[0].Units != 1 {
		t.Fatalf("want one processed output, got %+v", processes)
	}
}
