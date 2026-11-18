package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"transcoder/server/internal/compression"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/jobstate"
	"transcoder/server/internal/services/lifecycle"
	taskqueue "transcoder/server/internal/task"
	"transcoder/server/pkg/util"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidResult = errors.New("invalid execution result")

const (
	// StartTimeout bounds how long a dispatched attempt may wait for its first
	// RUNNING heartbeat before it is treated as lost and re-sent unchanged.
	StartTimeout = 10 * time.Minute
	// LeaseDuration is renewed by every heartbeat (workers send one every 20s).
	LeaseDuration = 2 * time.Minute
	// UnsentGrace is how old a never-sent attempt must be before recovery
	// sends it, so it does not race the dispatcher's own send.
	UnsentGrace = 30 * time.Second
	// FetchWait is how long a job waits before it is claimed again while a
	// sibling job is fetching their shared URL source.
	FetchWait     = 2 * time.Second
	dispatchBatch = 20

	DefaultMaxProcessingPerOwner = 8
	DefaultExecutionLimit        = 10 * time.Minute
	DefaultTargetExecutionLimit  = 30 * time.Minute
)

// Limits bound how much of the worker pool one user can hold and how long a
// single attempt may run. Zero fields take the defaults.
type Limits struct {
	// MaxProcessingPerOwner caps a user's PROCESSING jobs (queued in Redis or
	// running). A user at the cap is skipped by dispatch until one finishes.
	MaxProcessingPerOwner int
	// ExecutionLimit bounds an attempt from its first RUNNING heartbeat;
	// heartbeats never renew the lease past it.
	ExecutionLimit time.Duration
	// TargetExecutionLimit replaces ExecutionLimit for jobs with a target,
	// whose search encodes the image several times.
	TargetExecutionLimit time.Duration
}

func (l Limits) withDefaults() Limits {
	if l.MaxProcessingPerOwner <= 0 {
		l.MaxProcessingPerOwner = DefaultMaxProcessingPerOwner
	}
	if l.ExecutionLimit <= 0 {
		l.ExecutionLimit = DefaultExecutionLimit
	}
	if l.TargetExecutionLimit <= 0 {
		l.TargetExecutionLimit = DefaultTargetExecutionLimit
	}
	return l
}

func (l Limits) execution(job *models.Job) time.Duration {
	if job.Target != nil {
		return l.TargetExecutionLimit
	}
	return l.ExecutionLimit
}

type Service struct {
	db        *gorm.DB
	queue     taskqueue.Queue
	limits    Limits
	retention lifecycle.Policy
}

// NewService builds the orchestrator. retention sets expires_at on outputs as
// they are recorded; the API's lifecycle sweep catches any it leaves NULL.
func NewService(db *gorm.DB, queue taskqueue.Queue, limits Limits, retention lifecycle.Policy) *Service {
	return &Service{db: db, queue: queue, limits: limits.withDefaults(), retention: retention}
}

func RetryDelay(attempts int) time.Duration {
	return time.Duration(min(5*(1<<min(max(attempts-1, 0), 6)), 300)) * time.Second
}

// OutputKey is where an attempt's worker writes its result. Keys are per
// attempt, so a late attempt can never overwrite the published output.
func OutputKey(owner, job, attempt uuid.UUID, format string) string {
	return fmt.Sprintf("outputs/%s/%s/%s.%s", owner, job, attempt, extension(format))
}

func extension(format string) string {
	if format == "jpeg" {
		return "jpg"
	}
	return format
}

// dueQuery picks the next dispatch batch fairly across users. It walks the
// distinct owners of PENDING jobs with a skip scan, gives each owner at most
// as many jobs as it has room under MaxProcessingPerOwner, and interleaves
// the owners: every owner's first due job ranks before anyone's second, ties
// broken by how long the job has been due. A large backlog from one user
// therefore never delays another user's first job by more than one round.
//
// Status literals (not parameters) let the planner match the partial indexes
// idx_jobs_pending_owner and idx_jobs_processing_owner.
const dueQuery = `
WITH RECURSIVE owners(owner_id) AS (
	(SELECT owner_id FROM jobs WHERE status = 'PENDING' ORDER BY owner_id LIMIT 1)
	UNION ALL
	SELECT (SELECT j.owner_id FROM jobs j WHERE j.status = 'PENDING' AND j.owner_id > o.owner_id ORDER BY j.owner_id LIMIT 1)
	FROM owners o WHERE o.owner_id IS NOT NULL
), room AS (
	SELECT o.owner_id,
		@cap - (SELECT count(*) FROM jobs p WHERE p.owner_id = o.owner_id AND p.status = 'PROCESSING') AS free
	FROM owners o WHERE o.owner_id IS NOT NULL
)
SELECT c.id, r.owner_id
FROM room r
CROSS JOIN LATERAL (
	SELECT j.id, j.next_run_at, row_number() OVER (ORDER BY j.next_run_at, j.id) AS turn
	FROM jobs j
	WHERE j.owner_id = r.owner_id AND j.status = 'PENDING' AND j.next_run_at <= @now
	ORDER BY j.next_run_at, j.id
	LIMIT GREATEST(LEAST(r.free, @batch), 0)
) c
WHERE r.free > 0
ORDER BY c.turn, c.next_run_at, c.id
LIMIT @batch`

type candidate struct {
	ID      uuid.UUID
	OwnerID uuid.UUID
}

// DispatchDue claims due PENDING jobs and sends their attempts. A failed send
// leaves the attempt QUEUED and unsent, and Recover sends it later.
func (s *Service) DispatchDue(ctx context.Context, now time.Time) (int, error) {
	var due []candidate
	if err := s.db.WithContext(ctx).Raw(dueQuery, map[string]any{
		"cap": s.limits.MaxProcessingPerOwner, "now": now, "batch": dispatchBatch,
	}).Scan(&due).Error; err != nil {
		return 0, err
	}
	sent := 0
	for _, c := range due {
		attempt, err := s.claim(ctx, c, now)
		if err != nil {
			return sent, err
		}
		if attempt != nil {
			s.send(ctx, attempt)
			sent++
		}
	}
	return sent, nil
}

// claim turns one PENDING job into a QUEUED attempt. The per-owner advisory
// lock serializes claims for one user across orchestrator instances, so the
// PROCESSING count it re-checks cannot be raced past the cap. It is taken
// before the task → job locks and nothing else takes it, so it adds no
// deadlock cycle.
func (s *Service) claim(ctx context.Context, c candidate, now time.Time) (*models.Attempt, error) {
	var attempt *models.Attempt
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('lightyear:dispatch:' || ?, 0))`, c.OwnerID.String()).Error; err != nil {
			return err
		}
		job, err := jobstate.Lock(tx, c.ID)
		if err != nil {
			return err
		}
		if job.Status != models.JobPending || job.OwnerID != c.OwnerID || (job.NextRunAt != nil && job.NextRunAt.After(now)) {
			return nil
		}
		var processing int64
		if err := tx.Model(&models.Job{}).Where("owner_id = ? AND status = 'PROCESSING'", job.OwnerID).Count(&processing).Error; err != nil {
			return err
		}
		if processing >= int64(s.limits.MaxProcessingPerOwner) {
			return nil
		}
		var source models.File
		if err := tx.First(&source, "id = ? AND owner_id = ? AND status IN ?", job.SourceFileID, job.OwnerID,
			[]string{models.FileUploaded, models.FileFetching}).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return s.fail(tx, job, "source_unavailable", "source file is missing")
			}
			return err
		}
		sourceURL := ""
		if source.Status == models.FileFetching {
			if source.SourceURL == "" {
				return s.fail(tx, job, "source_unavailable", "source file is missing")
			}
			// A URL source is fetched by one job at a time; the other jobs
			// of the item wait and then read the stored object. Jobs become
			// PROCESSING only here, under the owner's lock, so the count holds.
			var fetching int64
			if err := tx.Model(&models.Job{}).
				Where("source_file_id = ? AND status = 'PROCESSING'", source.ID).Count(&fetching).Error; err != nil {
				return err
			}
			if fetching > 0 {
				return tx.Model(job).Update("next_run_at", now.Add(FetchWait)).Error
			}
			sourceURL = source.SourceURL
		}
		attemptID := util.NewUuidV7()
		outputKey := OutputKey(job.OwnerID, job.ID, attemptID, string(job.Options.Format))
		payload, err := json.Marshal(taskqueue.Dispatch{
			V: taskqueue.Version, JobID: job.ID.String(), AttemptID: attemptID.String(),
			Op: job.Operation, Options: job.Options, Target: job.Target, SourceKey: source.Path, SourceURL: sourceURL, OutputKey: outputKey,
		})
		if err != nil {
			return err
		}
		attempt = &models.Attempt{Base: models.Base{ID: attemptID}, JobID: job.ID, Status: models.AttemptQueued,
			Queue: taskqueue.QueueImage, Payload: models.JSON(payload), OutputKey: outputKey}
		if err := tx.Create(attempt).Error; err != nil {
			return err
		}
		if err := tx.Model(job).Updates(map[string]any{
			"status": models.JobProcessing, "current_attempt_id": attemptID, "attempts": job.Attempts + 1,
			"lease_expires_at": now.Add(StartTimeout), "next_run_at": nil, "progress": 0,
		}).Error; err != nil {
			return err
		}
		return jobstate.Aggregate(tx, job.TaskID)
	})
	return attempt, err
}

func (s *Service) send(ctx context.Context, attempt *models.Attempt) {
	if err := s.queue.Send(ctx, attempt.Queue, attempt.Payload); err != nil {
		slog.Warn("send attempt; recovery will retry", "attempt_id", attempt.ID, "error", err)
		return
	}
	if err := s.db.WithContext(ctx).Model(&models.Attempt{}).
		Where("id = ? AND dispatched_at IS NULL", attempt.ID).
		Update("dispatched_at", time.Now()).Error; err != nil {
		slog.Warn("record attempt dispatch", "attempt_id", attempt.ID, "error", err)
	}
}

// ProcessResult applies a worker heartbeat or outcome. Results for anything
// but the job's current, unfinished attempt are ignored.
func (s *Service) ProcessResult(ctx context.Context, r taskqueue.Result) error {
	if r.V != taskqueue.Version || (r.Progress != nil && (*r.Progress < 0 || *r.Progress > 100)) {
		return ErrInvalidResult
	}
	jobID, err := uuid.Parse(r.JobID)
	if err != nil {
		return ErrInvalidResult
	}
	attemptID, err := uuid.Parse(r.AttemptID)
	if err != nil {
		return ErrInvalidResult
	}
	now := time.Now()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		job, err := jobstate.Lock(tx, jobID)
		if err != nil {
			return err
		}
		if job.Status != models.JobProcessing || job.CurrentAttemptID == nil || *job.CurrentAttemptID != attemptID {
			return nil
		}
		var attempt models.Attempt
		if err := tx.First(&attempt, "id = ? AND job_id = ?", attemptID, jobID).Error; err != nil {
			return err
		}
		if r.Source != nil {
			if err := s.recordFetched(tx, job, *r.Source, now); err != nil {
				return err
			}
		}
		switch r.Status {
		case taskqueue.ResultRunning:
			updates := map[string]any{"status": models.AttemptRunning}
			started := now
			if attempt.StartedAt != nil {
				started = *attempt.StartedAt
			} else {
				updates["started_at"] = now
			}
			if err := tx.Model(&attempt).Updates(updates).Error; err != nil {
				return err
			}
			progress := job.Progress
			if r.Progress != nil {
				progress = max(progress, *r.Progress)
			}
			// The lease never outlives the execution limit; once it lapses,
			// Recover expires the attempt like any other lost lease.
			lease := now.Add(LeaseDuration)
			if deadline := started.Add(s.limits.execution(job)); lease.After(deadline) {
				lease = deadline
			}
			return tx.Model(job).Updates(map[string]any{"progress": progress, "lease_expires_at": lease}).Error
		case taskqueue.ResultCompleted:
			return s.complete(tx, job, &attempt, r, now)
		case taskqueue.ResultFailed:
			code, message, retryable := "internal_error", "worker reported failure", false
			if r.Error != nil {
				code, message, retryable = r.Error.Code, r.Error.Message, r.Error.Retryable
			}
			attemptUpdates := map[string]any{
				"status": models.AttemptFailed, "finished_at": now, "error_code": code,
			}
			if r.EncodeRounds != nil {
				attemptUpdates["encode_rounds"] = *r.EncodeRounds
			}
			if r.ComputeMS != nil {
				attemptUpdates["compute_ms"] = *r.ComputeMS
			}
			if err := tx.Model(&attempt).Updates(attemptUpdates).Error; err != nil {
				return err
			}
			return s.retryOrFail(tx, job, code, message, retryable, now)
		default:
			return ErrInvalidResult
		}
	})
}

// recordFetched marks a FETCHING source UPLOADED once the worker reports it
// fetched and stored the URL, whatever happens to the job afterwards, so a
// retry reads the stored object instead of fetching again. Lock order: job
// (held by the caller), then file.
func (s *Service) recordFetched(tx *gorm.DB, job *models.Job, src taskqueue.FetchedSource, now time.Time) error {
	var file models.File
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&file, "id = ? AND owner_id = ?", job.SourceFileID, job.OwnerID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if file.Status != models.FileFetching {
		// Reported already (by an earlier message of this attempt), or the
		// file was deleted meanwhile.
		return nil
	}
	if src.Key != file.Path || src.Size <= 0 || src.Mime == "" || len(src.Mime) > 127 {
		return fmt.Errorf("%w: fetched source invalid or outside the file", ErrInvalidResult)
	}
	if err := tx.Model(&file).Updates(map[string]any{
		"status": models.FileUploaded, "size": src.Size, "mime_type": src.Mime, "uploaded_at": now,
	}).Error; err != nil {
		return err
	}
	_, err := lifecycle.Schedule(tx, s.retention, file.ID)
	return err
}

func (s *Service) complete(tx *gorm.DB, job *models.Job, attempt *models.Attempt, r taskqueue.Result, now time.Time) error {
	artifact := r.Artifact
	if artifact == nil || artifact.Key != attempt.OutputKey || artifact.Size <= 0 ||
		artifact.Mime == "" || artifact.Width <= 0 || artifact.Height <= 0 {
		return fmt.Errorf("%w: artifact missing, invalid or outside the attempt", ErrInvalidResult)
	}
	var source models.File
	if err := tx.Select("name").First(&source, "id = ?", job.SourceFileID).Error; err != nil {
		return err
	}
	var siblingMulti int64
	if err := tx.Model(&models.Job{}).Where("task_id = ? AND output_index > 0", job.TaskID).Limit(1).Count(&siblingMulti).Error; err != nil {
		return err
	}
	name := compression.OutputFilename(source.Name, job.Options.Format, job.OutputName, job.OutputIndex, siblingMulti > 0)
	output := models.File{OwnerID: job.OwnerID, Name: name, Path: artifact.Key, Status: "UPLOADED",
		Size: artifact.Size, MimeType: artifact.Mime, Width: artifact.Width, Height: artifact.Height, UploadedAt: &now}
	if err := tx.Create(&output).Error; err != nil {
		return err
	}
	attemptUpdates := map[string]any{"status": models.AttemptCompleted, "finished_at": now}
	if r.EncodeRounds != nil {
		attemptUpdates["encode_rounds"] = *r.EncodeRounds
	}
	if r.ComputeMS != nil {
		attemptUpdates["compute_ms"] = *r.ComputeMS
	}
	if err := tx.Model(attempt).Updates(attemptUpdates).Error; err != nil {
		return err
	}
	resolved := job.Options
	if r.ResolvedOptions != nil {
		canonical, err := r.ResolvedOptions.Canonicalize()
		if err != nil || canonical.Format != job.Options.Format {
			return fmt.Errorf("%w: invalid resolved_options", ErrInvalidResult)
		}
		resolved = canonical
	}
	targetMet := r.TargetMet
	if targetMet == nil && job.Target != nil {
		targetMet = compression.IsTargetMet(artifact.Size, job.Target)
	}
	jobUpdates := map[string]any{
		"status":           models.JobCompleted,
		"progress":         100,
		"output_file_id":   output.ID,
		"resolved_options": resolved,
		"target_met":       targetMet,
		"lease_expires_at": nil,
		"error_code":       "",
		"error_message":    "",
	}
	if r.Achieved != nil {
		jobUpdates["achieved"] = *r.Achieved
	} else if job.Target != nil {
		jobUpdates["achieved"] = compression.Achieved{Bytes: new(artifact.Size)}
	} else {
		jobUpdates["achieved"] = nil
	}
	if err := tx.Model(job).Updates(jobUpdates).Error; err != nil {
		return err
	}
	// After output_file_id is set, so the file counts as an output.
	if _, err := lifecycle.Schedule(tx, s.retention, output.ID); err != nil {
		return err
	}
	return jobstate.Aggregate(tx, job.TaskID)
}

func (s *Service) fail(tx *gorm.DB, job *models.Job, code, message string) error {
	if err := tx.Model(job).Updates(map[string]any{
		"status": models.JobFailed, "error_code": code, "error_message": message,
		"lease_expires_at": nil, "next_run_at": nil,
	}).Error; err != nil {
		return err
	}
	return jobstate.Aggregate(tx, job.TaskID)
}

func (s *Service) retryOrFail(tx *gorm.DB, job *models.Job, code, message string, retryable bool, now time.Time) error {
	if !retryable || job.Attempts >= job.MaxAttempts {
		return s.fail(tx, job, code, message)
	}
	if err := tx.Model(job).Updates(map[string]any{
		"status": models.JobPending, "progress": 0, "current_attempt_id": nil, "lease_expires_at": nil,
		"next_run_at": now.Add(RetryDelay(job.Attempts)), "error_code": code, "error_message": message,
	}).Error; err != nil {
		return err
	}
	return jobstate.Aggregate(tx, job.TaskID)
}

// Recover handles lost messages and dead workers:
//   - an attempt that was never sent is sent;
//   - a sent attempt with no RUNNING heartbeat before StartTimeout is re-sent
//     as the same attempt, without consuming a retry;
//   - a RUNNING attempt whose lease expired is abandoned and the job retried.
//     Heartbeats cap the lease at the attempt's execution limit, so this is
//     also where an attempt that runs too long is stopped.
func (s *Service) Recover(ctx context.Context, now time.Time) error {
	var unsent []models.Attempt
	if err := s.db.WithContext(ctx).
		Where("status = ? AND dispatched_at IS NULL AND created_at < ?", models.AttemptQueued, now.Add(-UnsentGrace)).
		Order("created_at").Limit(100).Find(&unsent).Error; err != nil {
		return err
	}
	for i := range unsent {
		s.send(ctx, &unsent[i])
	}

	var expired []uuid.UUID
	if err := s.db.WithContext(ctx).Model(&models.Job{}).
		Where("status = ? AND lease_expires_at <= ?", models.JobProcessing, now).
		Order("lease_expires_at").Limit(100).Pluck("id", &expired).Error; err != nil {
		return err
	}
	for _, id := range expired {
		var resend *models.Attempt
		var abandoned *uuid.UUID
		if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			job, err := jobstate.Lock(tx, id)
			if err != nil {
				return err
			}
			if job.Status != models.JobProcessing || job.LeaseExpiresAt == nil || job.LeaseExpiresAt.After(now) || job.CurrentAttemptID == nil {
				return nil
			}
			var attempt models.Attempt
			if err := tx.First(&attempt, "id = ?", *job.CurrentAttemptID).Error; err != nil {
				return err
			}
			if attempt.Status == models.AttemptQueued {
				resend = &attempt
				return tx.Model(job).Update("lease_expires_at", now.Add(StartTimeout)).Error
			}
			if err := tx.Model(&attempt).Updates(map[string]any{"status": models.AttemptExpired, "finished_at": now}).Error; err != nil {
				return err
			}
			abandoned = &attempt.ID
			message := "execution lease expired"
			if attempt.StartedAt != nil && !now.Before(attempt.StartedAt.Add(s.limits.execution(job))) {
				message = "attempt exceeded the execution time limit"
			}
			return s.retryOrFail(tx, job, "execution_timeout", message, true, now)
		}); err != nil {
			return err
		}
		if resend != nil {
			s.send(ctx, resend)
		}
		if abandoned != nil {
			if err := s.queue.Cancel(ctx, abandoned.String()); err != nil {
				slog.Warn("signal abandoned attempt", "attempt_id", *abandoned, "error", err)
			}
		}
	}
	return nil
}
