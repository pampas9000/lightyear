package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/jobstate"
	taskqueue "transcoder/server/internal/task"
	"transcoder/server/pkg/util"

	"github.com/google/uuid"
	"gorm.io/gorm"
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
	UnsentGrace   = 30 * time.Second
	dispatchBatch = 20
)

type Service struct {
	db    *gorm.DB
	queue taskqueue.Queue
}

func NewService(db *gorm.DB, queue taskqueue.Queue) *Service {
	return &Service{db: db, queue: queue}
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

// DispatchDue claims due PENDING jobs and sends their attempts. A failed send
// leaves the attempt QUEUED and unsent, and Recover sends it later.
func (s *Service) DispatchDue(ctx context.Context, now time.Time) (int, error) {
	var ids []uuid.UUID
	if err := s.db.WithContext(ctx).Model(&models.Job{}).
		Where("status = ? AND (next_run_at IS NULL OR next_run_at <= ?)", models.JobPending, now).
		Order("next_run_at, id").Limit(dispatchBatch).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	sent := 0
	for _, id := range ids {
		attempt, err := s.claim(ctx, id, now)
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

func (s *Service) claim(ctx context.Context, id uuid.UUID, now time.Time) (*models.Attempt, error) {
	var attempt *models.Attempt
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		job, err := jobstate.Lock(tx, id)
		if err != nil {
			return err
		}
		if job.Status != models.JobPending || (job.NextRunAt != nil && job.NextRunAt.After(now)) {
			return nil
		}
		var source models.File
		if err := tx.First(&source, "id = ? AND owner_id = ? AND status = ?", job.SourceFileID, job.OwnerID, "UPLOADED").Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return s.fail(tx, job, "source_unavailable", "source file is missing")
			}
			return err
		}
		attemptID := util.NewUuidV7()
		outputKey := OutputKey(job.OwnerID, job.ID, attemptID, string(job.Options.Format))
		payload, err := json.Marshal(taskqueue.Dispatch{
			V: taskqueue.Version, JobID: job.ID.String(), AttemptID: attemptID.String(),
			Op: job.Operation, Options: job.Options, SourceKey: source.Path, OutputKey: outputKey,
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
		switch r.Status {
		case taskqueue.ResultRunning:
			updates := map[string]any{"status": models.AttemptRunning}
			if attempt.StartedAt == nil {
				updates["started_at"] = now
			}
			if err := tx.Model(&attempt).Updates(updates).Error; err != nil {
				return err
			}
			progress := job.Progress
			if r.Progress != nil {
				progress = max(progress, *r.Progress)
			}
			return tx.Model(job).Updates(map[string]any{"progress": progress, "lease_expires_at": now.Add(LeaseDuration)}).Error
		case taskqueue.ResultCompleted:
			return s.complete(tx, job, &attempt, r.Artifact, now)
		case taskqueue.ResultFailed:
			code, message, retryable := "internal_error", "worker reported failure", false
			if r.Error != nil {
				code, message, retryable = r.Error.Code, r.Error.Message, r.Error.Retryable
			}
			if err := tx.Model(&attempt).Updates(map[string]any{
				"status": models.AttemptFailed, "finished_at": now, "error_code": code,
			}).Error; err != nil {
				return err
			}
			return s.retryOrFail(tx, job, code, message, retryable, now)
		default:
			return ErrInvalidResult
		}
	})
}

func (s *Service) complete(tx *gorm.DB, job *models.Job, attempt *models.Attempt, artifact *taskqueue.Artifact, now time.Time) error {
	if artifact == nil || artifact.Key != attempt.OutputKey || artifact.Size <= 0 ||
		artifact.Mime == "" || artifact.Width <= 0 || artifact.Height <= 0 {
		return fmt.Errorf("%w: artifact missing, invalid or outside the attempt", ErrInvalidResult)
	}
	var source models.File
	if err := tx.Select("name").First(&source, "id = ?", job.SourceFileID).Error; err != nil {
		return err
	}
	name := strings.TrimSuffix(source.Name, filepath.Ext(source.Name)) + "." + extension(string(job.Options.Format))
	output := models.File{OwnerID: job.OwnerID, Name: name, Path: artifact.Key, Status: "UPLOADED",
		Size: artifact.Size, MimeType: artifact.Mime, Width: artifact.Width, Height: artifact.Height, UploadedAt: &now}
	if err := tx.Create(&output).Error; err != nil {
		return err
	}
	if err := tx.Model(attempt).Updates(map[string]any{"status": models.AttemptCompleted, "finished_at": now}).Error; err != nil {
		return err
	}
	if err := tx.Model(job).Updates(map[string]any{
		"status": models.JobCompleted, "progress": 100, "output_file_id": output.ID,
		"lease_expires_at": nil, "error_code": "", "error_message": "",
	}).Error; err != nil {
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
			return s.retryOrFail(tx, job, "execution_timeout", "execution lease expired", true, now)
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
