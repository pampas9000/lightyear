package usage

import (
	"errors"
	"sync"
	"testing"
	"time"

	"transcoder/server/internal/compression"
	"transcoder/server/internal/imageoptions"
	"transcoder/server/internal/models"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func ptr[T any](v T) *T { return &v }

var (
	fixed   = imageoptions.Options{Format: imageoptions.FormatWebP, Quality: ptr(80)}
	search  = imageoptions.Options{Format: imageoptions.FormatWebP}
	jxl     = imageoptions.Options{Format: imageoptions.FormatJXL}
	lossy   = &compression.Target{MaxBytes: ptr[int64](50_000)}
	checked = imageoptions.Options{Format: imageoptions.FormatAVIF, Quality: ptr(60)}
	lossls  = imageoptions.Options{Format: imageoptions.FormatWebP, Lossless: true}
)

func addJob(t *testing.T, db *gorm.DB, owner uuid.UUID, status models.JobStatus,
	opts imageoptions.Options, target *compression.Target) models.Job {
	t.Helper()
	j := models.Job{OwnerID: owner, TaskID: uuid.New(), Operation: models.OpImageConvert,
		Options: opts, Target: target, SourceFileID: uuid.New(), Status: status}
	if err := db.Create(&j).Error; err != nil {
		t.Fatal(err)
	}
	return j
}

func addUsage(t *testing.T, db *gorm.DB, owner uuid.UUID, kind string, units int64, at time.Time) {
	t.Helper()
	rec := models.UsageRecord{OwnerID: owner, Kind: kind, JobID: uuid.New(), TaskID: uuid.New(),
		Units: units, CreatedAt: at}
	if kind != models.UsageReuse {
		rec.AttemptID = ptr(uuid.New())
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
}

func addUser(t *testing.T, db *gorm.DB, monthly, storage *int64) uuid.UUID {
	t.Helper()
	u := models.User{Username: uuid.NewString(), Email: uuid.NewString() + "@example.com",
		MonthlyUnitQuota: monthly, StorageQuotaBytes: storage}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestEstimateSQLMatchesGo(t *testing.T) {
	db := testutil.OpenDatabase(t)
	cases := []struct {
		opts   imageoptions.Options
		target *compression.Target
		want   int64
	}{
		{fixed, nil, 1},
		{search, lossy, SearchEstimate},
		{jxl, lossy, JXLSearchEstimate},
		{checked, lossy, 1},
		{lossls, lossy, 1},
		{search, &compression.Target{}, 1},
	}
	for i, c := range cases {
		if got := EstimateUnits(c.opts, c.target); got != c.want {
			t.Errorf("case %d: EstimateUnits = %d, want %d", i, got, c.want)
		}
		owner := uuid.New()
		addJob(t, db, owner, models.JobPending, c.opts, c.target)
		got, err := reserved(db, owner)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("case %d: SQL estimate = %d, want %d", i, got, c.want)
		}
	}
}

func TestRecordAttemptIsIdempotent(t *testing.T) {
	db := testutil.OpenDatabase(t)
	job := addJob(t, db, uuid.New(), models.JobProcessing, search, lossy)
	attempt := uuid.New()
	for range 2 {
		if err := RecordAttempt(db, models.UsageProcess, &job, attempt, ptr(5), ptr[int64](900), nil); err != nil {
			t.Fatal(err)
		}
	}
	// The same attempt may also have a fetch, which is another kind.
	if err := RecordFetch(db, &job, attempt, 10); err != nil {
		t.Fatal(err)
	}
	var recs []models.UsageRecord
	db.Order("kind").Find(&recs)
	if len(recs) != 2 || recs[1].Kind != models.UsageProcess || recs[1].Units != 5 {
		t.Fatalf("unexpected records: %+v", recs)
	}
}

func TestCheckWorkCountsSettledAndReservedUnits(t *testing.T) {
	db := testutil.OpenDatabase(t)
	q := NewQuotas(db, Limits{MonthlyUnits: 1000})
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	q.now = func() time.Time { return now }
	owner := addUser(t, db, ptr[int64](10), nil)
	addUsage(t, db, owner, models.UsageProcess, 3, now.Add(-time.Hour))
	addUsage(t, db, owner, models.UsageProcess, 500, time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)) // last month
	addJob(t, db, owner, models.JobProcessing, fixed, nil)                                          // reserves 1

	// 3 settled + 1 reserved + 1 requested fits under 10.
	addJob(t, db, owner, models.JobPending, fixed, nil)
	if err := q.CheckWork(db, owner, 1); err != nil {
		t.Fatalf("5 of 10 units must fit: %v", err)
	}
	// A search reserves 7 more: 12 > 10.
	addJob(t, db, owner, models.JobPending, search, lossy)
	err := q.CheckWork(db, owner, SearchEstimate)
	var qe *QuotaError
	if !errors.As(err, &qe) || !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("want a quota error, got %v", err)
	}
	if qe.Quota != QuotaMonthlyUnits || qe.Limit != 10 || qe.Used != 3 || qe.Reserved != 2 || qe.Requested != 7 {
		t.Fatalf("unexpected quota error: %+v", qe)
	}
	// Work that reserves nothing (all reused) is never refused.
	if err := q.CheckWork(db, owner, 0); err != nil {
		t.Fatal(err)
	}
	// 0 is unlimited.
	db.Model(&models.User{}).Where("id = ?", owner).Update("monthly_unit_quota", 0)
	if err := q.CheckWork(db, owner, SearchEstimate); err != nil {
		t.Fatalf("unlimited user refused: %v", err)
	}
}

func TestDefaultLimitsApplyWithoutOverride(t *testing.T) {
	db := testutil.OpenDatabase(t)
	q := NewQuotas(db, Limits{MonthlyUnits: 2})
	owner := addUser(t, db, nil, nil)
	addJob(t, db, owner, models.JobPending, search, lossy)
	if err := q.CheckWork(db, owner, SearchEstimate); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("global limit not applied: %v", err)
	}
}

func TestConcurrentWorkCannotPassTheQuotaTogether(t *testing.T) {
	db := testutil.OpenDatabase(t)
	q := NewQuotas(db, Limits{})
	owner := addUser(t, db, ptr[int64](10), nil)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			errs <- db.Transaction(func(tx *gorm.DB) error {
				addJob(t, tx, owner, models.JobPending, search, lossy) // 7 units each
				return q.CheckWork(tx, owner, SearchEstimate)
			})
		})
	}
	wg.Wait()
	close(errs)
	accepted := 0
	for err := range errs {
		switch {
		case err == nil:
			accepted++
		case !errors.Is(err, ErrQuotaExceeded):
			t.Fatal(err)
		}
	}
	var pending int64
	db.Model(&models.Job{}).Where("owner_id = ?", owner).Count(&pending)
	if accepted != 1 || pending != 1 {
		t.Fatalf("accepted %d requests leaving %d jobs; want exactly one", accepted, pending)
	}
}

func TestStorageQuota(t *testing.T) {
	db := testutil.OpenDatabase(t)
	q := NewQuotas(db, Limits{StorageBytes: 1000})
	owner := addUser(t, db, nil, nil)
	for _, f := range []models.File{
		{Status: models.FileUploaded, Size: 600},
		{Status: models.FileUploading, Size: 300}, // declared size of an upload in progress
		{Status: models.FileExpired, Size: 5000},  // no longer stored
	} {
		f.OwnerID, f.Name, f.Path = owner, "a.png", uuid.NewString()
		if err := db.Create(&f).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := q.CheckUpload(t.Context(), owner, 100); err != nil {
		t.Fatalf("900 + 100 of 1000 must fit: %v", err)
	}
	err := q.CheckUpload(t.Context(), owner, 101)
	var qe *QuotaError
	if !errors.As(err, &qe) || qe.Quota != QuotaStorage || qe.Used != 900 || qe.Requested != 101 {
		t.Fatalf("want a storage quota error, got %v", err)
	}
	// Work is refused only once storage is full.
	if err := q.CheckWork(db, owner, 1); err != nil {
		t.Fatal(err)
	}
	db.Create(&models.File{OwnerID: owner, Name: "b.png", Path: uuid.NewString(), Status: models.FileUploaded, Size: 100})
	if err := q.CheckWork(db, owner, 1); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("full storage must refuse work, got %v", err)
	}
}

func TestSummary(t *testing.T) {
	db := testutil.OpenDatabase(t)
	q := NewQuotas(db, Limits{MonthlyUnits: 100})
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	q.now = func() time.Time { return now }
	owner := addUser(t, db, nil, ptr[int64](0))
	addUsage(t, db, owner, models.UsageProcess, 7, time.Date(2026, 11, 2, 23, 59, 0, 0, time.UTC))
	addUsage(t, db, owner, models.UsageProcess, 1, time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC))
	addUsage(t, db, owner, models.UsageReuse, 0, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	addUsage(t, db, owner, models.UsageFailed, 0, time.Date(2026, 11, 3, 0, 0, 1, 0, time.UTC))
	addUsage(t, db, owner, models.UsageProcess, 40, time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC))
	addUsage(t, db, uuid.New(), models.UsageProcess, 9, now) // someone else
	addJob(t, db, owner, models.JobPending, jxl, lossy)

	s, err := q.Summary(t.Context(), owner, now)
	if err != nil {
		t.Fatal(err)
	}
	if s.Month != "2026-11" || s.Units.Used != 8 || s.Units.Reserved != JXLSearchEstimate ||
		s.Units.Limit == nil || *s.Units.Limit != 100 || s.Storage.LimitBytes != nil {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if s.Totals.Outputs != 2 || s.Totals.ReusedOutputs != 1 || s.Totals.FailedAttempts != 1 {
		t.Fatalf("unexpected totals: %+v", s.Totals)
	}
	if len(s.Daily) != 2 || s.Daily[0].Date != "2026-11-02" || s.Daily[0].Units != 8 ||
		s.Daily[1].Date != "2026-11-03" || s.Daily[1].ReusedOutputs != 1 || s.Daily[1].FailedAttempts != 1 {
		t.Fatalf("unexpected days: %+v", s.Daily)
	}

	past, err := q.Summary(t.Context(), owner, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if past.Units.Used != 40 || past.Units.Reserved != 0 || len(past.Daily) != 1 {
		t.Fatalf("unexpected past month: %+v", past)
	}
}
