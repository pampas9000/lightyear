// Package usage meters processing and enforces per-user quotas.
//
// The ledger is usage_records: one row per metered event, written in the
// same transaction as the state change it meters (an attempt completing or
// failing, a job reusing an output, a URL source being fetched). Quotas and
// GET /v1/usage are sums over it, so they cannot drift from what was done.
//
// A unit is one encode round. An ordinary convert is one round; a target
// search encodes several. Units are settled from the worker's reported
// encode_rounds when an attempt completes; while a job is unfinished it is
// reserved at an estimate (see EstimateUnits), so a quota can be checked when
// work is accepted.
package usage

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"transcoder/server/internal/compression"
	"transcoder/server/internal/imageoptions"
	"transcoder/server/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Upper bounds of the encode rounds of a target search, used to reserve
// units before a job runs. They match the binary searches in
// compute-optimize: 100 quality levels, and JXL's distance levels plus the
// lossless JPEG transcode it tries first.
const (
	SearchEstimate    int64 = 7
	JXLSearchEstimate int64 = 9
)

// EstimateUnits is the units reserved for an unfinished job: 1 unless the
// job searches for a target, which is bounded by the search's rounds.
func EstimateUnits(opts imageoptions.Options, target *compression.Target) int64 {
	if !compression.NeedsSearch(opts, target) {
		return 1
	}
	if opts.Format == imageoptions.FormatJXL {
		return JXLSearchEstimate
	}
	return SearchEstimate
}

// estimateSQL is EstimateUnits over the jobs table, for reserving the units
// of unfinished jobs. TestEstimateSQLMatchesGo keeps the two in step.
var estimateSQL = fmt.Sprintf(`CASE WHEN jobs.target->>'max_bytes' IS NOT NULL
	AND jobs.options->'quality' IS NULL AND jobs.options->'distance' IS NULL
	AND (jobs.options->>'lossless')::boolean IS NOT TRUE
	THEN CASE WHEN jobs.options->>'format' = 'jxl' THEN %d ELSE %d END ELSE 1 END`,
	JXLSearchEstimate, SearchEstimate)

// unfinished are the job statuses whose units are reserved.
var unfinished = []models.JobStatus{models.JobPending, models.JobProcessing}

// RecordAttempt meters an attempt that completed (UsageProcess) or that the
// worker reported failed (UsageFailed). A completed attempt costs its
// encode rounds, at least 1; a failed one costs nothing. Recording the same
// attempt again is a no-op.
func RecordAttempt(tx *gorm.DB, kind string, job *models.Job, attemptID uuid.UUID,
	rounds *int, computeMS *int64, output *models.File) error {
	rec := models.UsageRecord{
		OwnerID: job.OwnerID, Kind: kind, AttemptID: &attemptID, JobID: job.ID, TaskID: job.TaskID,
	}
	if rounds != nil {
		rec.EncodeRounds = max(*rounds, 0)
	}
	if computeMS != nil {
		rec.ComputeMS = max(*computeMS, 0)
	}
	if kind == models.UsageProcess {
		rec.Units = int64(max(rec.EncodeRounds, 1))
		estimate := EstimateUnits(job.Options, job.Target)
		if rec.Units != estimate {
			slog.Debug("usage differs from estimate", "attempt_id", attemptID, "job_id", job.ID,
				"units", rec.Units, "estimate", estimate, "format", job.Options.Format)
		}
	}
	if output != nil {
		rec.OutputBytes = output.Size
		rec.OutputPixels = int64(output.Width) * int64(output.Height)
	}
	return create(tx, &rec)
}

// RecordReuse meters a job completed with an existing output: no units.
func RecordReuse(tx *gorm.DB, job *models.Job, output *models.File) error {
	rec := models.UsageRecord{OwnerID: job.OwnerID, Kind: models.UsageReuse, JobID: job.ID, TaskID: job.TaskID}
	if output != nil {
		rec.OutputBytes = output.Size
		rec.OutputPixels = int64(output.Width) * int64(output.Height)
	}
	return create(tx, &rec)
}

// RecordFetch meters a URL source an attempt downloaded: no units.
func RecordFetch(tx *gorm.DB, job *models.Job, attemptID uuid.UUID, size int64) error {
	return create(tx, &models.UsageRecord{OwnerID: job.OwnerID, Kind: models.UsageFetch,
		AttemptID: &attemptID, JobID: job.ID, TaskID: job.TaskID, InputBytes: size})
}

func create(tx *gorm.DB, rec *models.UsageRecord) error {
	err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(rec).Error
	if err != nil {
		slog.Error("record usage failed", "kind", rec.Kind, "attempt_id", rec.AttemptID,
			"job_id", rec.JobID, "owner_id", rec.OwnerID, "error", err)
		return fmt.Errorf("record usage: %w", err)
	}
	return nil
}

// MonthStart is the start of the calendar month (UTC) containing t.
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// unitsSince sums the owner's settled units since start.
func unitsSince(tx *gorm.DB, owner uuid.UUID, start time.Time) (int64, error) {
	var n int64
	err := tx.Model(&models.UsageRecord{}).Where("owner_id = ? AND created_at >= ?", owner, start).
		Select("COALESCE(SUM(units), 0)").Scan(&n).Error
	return n, err
}

// reserved sums the estimated units of the owner's unfinished jobs.
func reserved(tx *gorm.DB, owner uuid.UUID) (int64, error) {
	var n int64
	err := tx.Model(&models.Job{}).Where("owner_id = ? AND status IN ?", owner, unfinished).
		Select("COALESCE(SUM(" + estimateSQL + "), 0)").Scan(&n).Error
	return n, err
}

// storageStatuses are the file statuses that take storage. An UPLOADING
// file counts its declared size, so parallel uploads cannot pass the quota
// together; a FETCHING file has no size until it is fetched.
var storageStatuses = []string{models.FileUploaded, models.FileUploading}

// StorageUsed is the bytes the owner's stored files take.
func StorageUsed(tx *gorm.DB, owner uuid.UUID) (int64, error) {
	var n int64
	err := tx.Model(&models.File{}).Where("owner_id = ? AND status IN ?", owner, storageStatuses).
		Select("COALESCE(SUM(size), 0)").Scan(&n).Error
	return n, err
}

// ErrQuotaExceeded matches every *QuotaError.
var ErrQuotaExceeded = errors.New("quota exceeded")

// Quota names, as in QuotaError.Quota and error details.
const (
	QuotaStorage      = "storage"
	QuotaMonthlyUnits = "monthly_units"
)

// QuotaError reports which quota a request would exceed.
type QuotaError struct {
	Quota string
	Limit int64
	// Used is settled usage: units this month, or stored bytes.
	Used int64
	// Reserved is the estimated units of other unfinished jobs.
	Reserved  int64
	Requested int64
}

func (e *QuotaError) Error() string {
	if e.Quota == QuotaStorage {
		return fmt.Sprintf("storage quota exceeded: %d of %d bytes used, %d requested", e.Used, e.Limit, e.Requested)
	}
	return fmt.Sprintf("monthly unit quota exceeded: %d used and %d reserved of %d, %d requested",
		e.Used, e.Reserved, e.Limit, e.Requested)
}

func (e *QuotaError) Is(target error) bool { return target == ErrQuotaExceeded }

func reject(owner uuid.UUID, e *QuotaError) error {
	slog.Info("quota exceeded", "owner_id", owner, "quota", e.Quota, "limit", e.Limit,
		"used", e.Used, "reserved", e.Reserved, "requested", e.Requested)
	return e
}
