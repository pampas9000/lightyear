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
	"transcoder/server/internal/services/usage"
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
	// UnsentGrace is how long the relay leaves a new outbox message to the
	// send its writer makes right after commit, so the two rarely both send.
	UnsentGrace = 5 * time.Second
	// RelayClaim is how long a relay instance owns the messages it claimed;
	// if it dies mid-batch, another instance sends them after this.
	RelayClaim = 30 * time.Second
	// OutboxRetention is how long sent messages are kept for diagnosis.
	OutboxRetention = 7 * 24 * time.Hour
	// PruneInterval is how often sent messages past OutboxRetention are deleted.
	PruneInterval = time.Hour
	relayBatch    = 100
	pruneBatch    = 1000
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
// leaves the outbox message unsent, and Relay sends it later.
func (s *Service) DispatchDue(ctx context.Context, now time.Time) (int, error) {
	var due []candidate
	if err := s.db.WithContext(ctx).Raw(dueQuery, map[string]any{
		"cap": s.limits.MaxProcessingPerOwner, "now": now, "batch": dispatchBatch,
	}).Scan(&due).Error; err != nil {
		return 0, err
	}
	claimed := 0
	for _, c := range due {
		msg, err := s.claim(ctx, c, now)
		if err != nil {
			return claimed, err
		}
		if msg != nil {
			s.deliver(ctx, msg)
			claimed++
		}
	}
	return claimed, nil
}

// claim turns one PENDING job into a QUEUED attempt and the outbox message
// that sends it. The per-owner advisory lock serializes claims for one user
// across orchestrator instances, so the PROCESSING count it re-checks cannot
// be raced past the cap. It is taken before the task → job locks and nothing
// else takes it, so it adds no deadlock cycle.
func (s *Service) claim(ctx context.Context, c candidate, now time.Time) (*models.OutboxMessage, error) {
	var msg *models.OutboxMessage
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
		attempt := &models.Attempt{Base: models.Base{ID: attemptID}, JobID: job.ID, Status: models.AttemptQueued,
			Queue: taskqueue.QueueImage, Payload: models.JSON(payload), OutputKey: outputKey}
		if err := tx.Create(attempt).Error; err != nil {
			return err
		}
		if msg, err = enqueue(tx, attempt, now); err != nil {
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
	return msg, err
}

// enqueue writes an outbox message that sends attempt as dispatched. It must
// run in the transaction that makes the send necessary.
func enqueue(tx *gorm.DB, attempt *models.Attempt, now time.Time) (*models.OutboxMessage, error) {
	msg := &models.OutboxMessage{CreatedAt: now, Queue: attempt.Queue, Payload: attempt.Payload,
		AttemptID: &attempt.ID, AvailableAt: now.Add(UnsentGrace)}
	return msg, tx.Create(msg).Error
}

// deliver sends one outbox message and records the outcome. A failed send
// stays unsent and becomes available to the relay again after a backoff.
func (s *Service) deliver(ctx context.Context, msg *models.OutboxMessage) {
	db := s.db.WithContext(ctx).Model(&models.OutboxMessage{}).Where("id = ? AND sent_at IS NULL", msg.ID)
	streamID, err := s.queue.Send(ctx, msg.Queue, msg.Payload)
	if err != nil {
		slog.Warn("send outbox message; relay will retry", "message_id", msg.ID, "attempt_id", msg.AttemptID, "error", err)
		tries := msg.Tries + 1
		if err := db.Updates(map[string]any{
			"tries": tries, "last_error": truncate(err.Error(), 255), "available_at": time.Now().Add(relayBackoff(tries)),
		}).Error; err != nil {
			slog.Warn("record outbox send failure", "message_id", msg.ID, "error", err)
		}
		return
	}
	if err := db.Updates(map[string]any{"sent_at": time.Now(), "tries": msg.Tries + 1, "stream_id": streamID}).Error; err != nil {
		slog.Warn("record outbox send", "message_id", msg.ID, "error", err)
	}
}

// relayBackoff is 1s after the first failed send, doubling up to a minute.
func relayBackoff(tries int) time.Duration {
	return time.Duration(min(1<<min(max(tries-1, 0), 6), 60)) * time.Second
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// relayQuery claims a batch of due unsent messages by pushing their
// available_at past RelayClaim, so concurrent relays take disjoint batches
// and a relay that dies leaves its batch to the next one. The literal
// predicate lets the planner use idx_outbox_unsent.
const relayQuery = `
UPDATE outbox_messages SET available_at = @claim
WHERE id IN (
	SELECT id FROM outbox_messages
	WHERE sent_at IS NULL AND available_at <= @now
	ORDER BY available_at, id
	LIMIT @batch
	FOR UPDATE SKIP LOCKED
)
RETURNING *`

// Relay sends outbox messages whose writer did not send them: the send after
// commit failed, or the process stopped before making it.
func (s *Service) Relay(ctx context.Context, now time.Time) (int, error) {
	var due []models.OutboxMessage
	if err := s.db.WithContext(ctx).Raw(relayQuery, map[string]any{
		"claim": now.Add(RelayClaim), "now": now, "batch": relayBatch,
	}).Scan(&due).Error; err != nil {
		return 0, err
	}
	for i := range due {
		s.deliver(ctx, &due[i])
	}
	return len(due), nil
}

// PruneOutbox deletes sent messages older than OutboxRetention. It deletes in
// batches, so no single statement holds locks on a large backlog, and returns
// how many it deleted.
func (s *Service) PruneOutbox(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for ctx.Err() == nil {
		res := s.db.WithContext(ctx).Exec(`
DELETE FROM outbox_messages WHERE id IN (
	SELECT id FROM outbox_messages WHERE sent_at IS NOT NULL AND sent_at < ? LIMIT ?
)`, now.Add(-OutboxRetention), pruneBatch)
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if res.RowsAffected < pruneBatch {
			break
		}
	}
	return total, ctx.Err()
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
	completed := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
			if err := s.recordFetched(tx, job, attemptID, *r.Source, now); err != nil {
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
			if err := s.complete(tx, job, &attempt, r, now); err != nil {
				return err
			}
			completed = true
			return nil
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
			if err := usage.RecordAttempt(tx, models.UsageFailed, job, attemptID, r.EncodeRounds, r.ComputeMS, nil); err != nil {
				return err
			}
			return s.retryOrFail(tx, job, code, message, retryable, now)
		default:
			return ErrInvalidResult
		}
	})
	if err == nil && completed {
		// Another copy of this attempt may still be queued or running (a
		// re-send, or the relay and the writer both sending). Its output key
		// is the published object's, so it must not upload: a copy not yet
		// started skips the attempt, a running one is killed within a cancel
		// poll. Best effort, like every cancel signal.
		if err := s.queue.Cancel(ctx, attemptID.String()); err != nil {
			slog.Warn("signal completed attempt", "attempt_id", attemptID, "error", err)
		}
	}
	return err
}

// recordFetched marks a FETCHING source UPLOADED once the worker reports it
// fetched and stored the URL, whatever happens to the job afterwards, so a
// retry reads the stored object instead of fetching again. Lock order: job
// (held by the caller), then file.
func (s *Service) recordFetched(tx *gorm.DB, job *models.Job, attemptID uuid.UUID, src taskqueue.FetchedSource, now time.Time) error {
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
	// expires_at is cleared so Schedule counts retention from the fetch, not
	// from when the URL was submitted (the sweep may have scheduled it).
	if err := tx.Model(&file).Updates(map[string]any{
		"status": models.FileUploaded, "size": src.Size, "mime_type": src.Mime, "uploaded_at": now,
		"expires_at": nil,
	}).Error; err != nil {
		return err
	}
	if err := usage.RecordFetch(tx, job, attemptID, src.Size); err != nil {
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
	if err := usage.RecordAttempt(tx, models.UsageProcess, job, attempt.ID, r.EncodeRounds, r.ComputeMS, &output); err != nil {
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

// Recover handles lost messages and dead workers (sends that never reached
// the queue are Relay's):
//   - an attempt with no RUNNING heartbeat before StartTimeout is re-sent as
//     the same attempt through the outbox, without consuming a retry, unless
//     its last send is still waiting in the stream (a backlog, not a loss):
//     then only the lease is renewed;
//   - a RUNNING attempt whose lease expired is abandoned and the job retried.
//     Heartbeats cap the lease at the attempt's execution limit, so this is
//     also where an attempt that runs too long is stopped.
func (s *Service) Recover(ctx context.Context, now time.Time) error {
	var expired []uuid.UUID
	if err := s.db.WithContext(ctx).Model(&models.Job{}).
		Where("status = ? AND lease_expires_at <= ?", models.JobProcessing, now).
		Order("lease_expires_at").Limit(100).Pluck("id", &expired).Error; err != nil {
		return err
	}
	for _, id := range expired {
		waiting, err := s.stillWaiting(ctx, id)
		if err != nil {
			return err
		}
		var resend *models.OutboxMessage
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
				// A send still pending (the queue has been down) already
				// covers the re-send; adding another would only duplicate it.
				var pending int64
				if err := tx.Model(&models.OutboxMessage{}).
					Where("attempt_id = ? AND sent_at IS NULL", attempt.ID).Count(&pending).Error; err != nil {
					return err
				}
				if pending == 0 && !waiting {
					if resend, err = enqueue(tx, &attempt, now); err != nil {
						return err
					}
				}
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
			s.deliver(ctx, resend)
		}
		if abandoned != nil {
			if err := s.queue.Cancel(ctx, abandoned.String()); err != nil {
				slog.Warn("signal abandoned attempt", "attempt_id", *abandoned, "error", err)
			}
		}
	}
	return nil
}

// lastSendQuery finds the latest recorded send of a job's current attempt
// while that attempt is still QUEUED.
const lastSendQuery = `
SELECT o.* FROM outbox_messages o
JOIN jobs j ON j.current_attempt_id = o.attempt_id
JOIN attempts a ON a.id = o.attempt_id
WHERE j.id = ? AND a.status = ? AND o.sent_at IS NOT NULL AND o.stream_id <> ''
ORDER BY o.sent_at DESC, o.id DESC
LIMIT 1`

// stillWaiting reports whether the latest send of job's QUEUED attempt is
// still in the stream and not yet delivered to a worker: the queue is backed
// up, the message is not lost, and re-sending would only queue a duplicate.
// Anything else (no recorded stream ID, the entry trimmed or lost with the
// stream or group, or delivered to a worker that never reported RUNNING)
// is false, and Recover re-sends.
//
// When Redis cannot answer it reports true, so Recover only renews the
// lease: a re-send could not reach Redis either, and the next check after
// StartTimeout decides once Redis answers.
//
// It runs before Recover locks the job, so no row lock is held across Redis
// calls; Recover re-checks the job and lease under the lock, and a concurrent
// re-send or heartbeat moves the lease and so skips this round.
func (s *Service) stillWaiting(ctx context.Context, jobID uuid.UUID) (bool, error) {
	var msgs []models.OutboxMessage
	if err := s.db.WithContext(ctx).Raw(lastSendQuery, jobID, models.AttemptQueued).Scan(&msgs).Error; err != nil {
		return false, err
	}
	if len(msgs) == 0 {
		return false, nil
	}
	msg := msgs[0]
	waiting, err := s.queue.Waiting(ctx, msg.Queue, msg.StreamID)
	if err != nil {
		slog.Warn("check queued dispatch; renewing the lease without re-sending",
			"attempt_id", msg.AttemptID, "stream_id", msg.StreamID, "error", err)
		return true, nil
	}
	return waiting, nil
}
