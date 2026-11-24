package task

import (
	"encoding/json"
	"errors"
	"testing"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/usage"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
)

// searchWebP searches for a target: it reserves usage.SearchEstimate units.
var searchWebP = json.RawMessage(`[{"options":{"format":"webp"},"target":{"max_bytes":50000}}]`)

func TestCreateOverMonthlyQuotaCreatesNothing(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	s := NewService(db, nil).WithQuotas(usage.NewQuotas(db, usage.Limits{MonthlyUnits: 8}))
	file := uploadedFile(t, db, owner)
	items := []TaskItemInput{{FileID: file.ID}}

	if _, err := s.CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, Items: items, Outputs: searchWebP}); err != nil {
		t.Fatalf("7 of 8 units must fit: %v", err)
	}
	// A second search reserves 7 more; the first is still pending.
	again := uploadedFile(t, db, owner)
	_, err := s.CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: again.ID}},
		Outputs: searchWebP, IdempotencyKey: "k"})
	var qe *usage.QuotaError
	if !errors.As(err, &qe) || qe.Quota != usage.QuotaMonthlyUnits || qe.Reserved != 7 || qe.Requested != 7 {
		t.Fatalf("want a monthly quota error, got %v", err)
	}
	var tasks, jobs int64
	db.Model(&models.Task{}).Where("owner_id = ?", owner).Count(&tasks)
	db.Model(&models.Job{}).Where("owner_id = ?", owner).Count(&jobs)
	if tasks != 1 || jobs != 1 {
		t.Fatalf("a refused request must create nothing, have %d tasks and %d jobs", tasks, jobs)
	}
	// The refused source can still be used: an ordinary convert (1 unit) fits.
	if _, err := s.CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: again.ID}},
		Options: avif(70)}); err != nil {
		t.Fatalf("1 more unit must fit: %v", err)
	}
}

func TestReusedOutputIsRecordedAndReservesNothing(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	s := NewService(db, nil)
	file := uploadedFile(t, db, owner)
	first, err := s.CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: file.ID}}, Options: avif(70)})
	if err != nil {
		t.Fatal(err)
	}
	output := models.File{OwnerID: owner, Name: "source.avif", Path: "outputs/x.avif", Status: "UPLOADED",
		Size: 2048, Width: 10, Height: 20}
	db.Create(&output)
	db.Model(&models.Job{}).Where("id = ?", first.Jobs[0].ID).
		Updates(completedWith(output.ID))

	// The user is now out of units, but a fully reused task costs none.
	s.WithQuotas(usage.NewQuotas(db, usage.Limits{MonthlyUnits: 1}))
	db.Create(&models.UsageRecord{OwnerID: owner, Kind: models.UsageProcess, AttemptID: new(uuid.New()),
		JobID: first.Jobs[0].ID, TaskID: first.ID, Units: 1})
	again, err := s.CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: file.ID}}, Options: avif(70)})
	if err != nil {
		t.Fatalf("a reused task must not be refused: %v", err)
	}
	var rec models.UsageRecord
	if err := db.First(&rec, "kind = ? AND job_id = ?", models.UsageReuse, again.Jobs[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if rec.Units != 0 || rec.OutputBytes != 2048 || rec.OutputPixels != 200 || rec.TaskID != again.ID {
		t.Fatalf("unexpected reuse record: %+v", rec)
	}
	if _, err := s.CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: file.ID}}, Options: avif(50)}); !errors.Is(err, usage.ErrQuotaExceeded) {
		t.Fatalf("new work must be refused, got %v", err)
	}
}

func TestRetryIsCheckedAgainstTheQuota(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	s := NewService(db, nil)
	file := uploadedFile(t, db, owner)
	created, err := s.CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: file.ID}}, Outputs: searchWebP})
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&models.Job{}).Where("task_id = ?", created.ID).Updates(failed)
	s.WithQuotas(usage.NewQuotas(db, usage.Limits{MonthlyUnits: 6}))
	if _, err := s.RetryTask(t.Context(), RetryTaskInput{OwnerID: owner, TaskID: created.ID}); !errors.Is(err, usage.ErrQuotaExceeded) {
		t.Fatalf("retrying a 7-unit search under 6 units must be refused, got %v", err)
	}
	var job models.Job
	db.First(&job, "task_id = ?", created.ID)
	if job.Status != models.JobFailed {
		t.Fatalf("refused retry changed the job: %s", job.Status)
	}
}
