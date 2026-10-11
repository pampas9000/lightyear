package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"transcoder/server/internal/execution"
	"transcoder/server/internal/models"
	"transcoder/server/internal/operation"
	"transcoder/server/internal/services/jobstate"
	"transcoder/server/internal/services/outbox"
	taskqueue "transcoder/server/internal/task"
	"transcoder/server/pkg/util"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrInvalidResult = errors.New("invalid execution result")

const LeaseDuration = 5 * time.Minute

type Service struct {
	db        *gorm.DB
	executors execution.Registry
}

func NewService(db *gorm.DB, executors execution.Registry) *Service {
	return &Service{db: db, executors: executors}
}

func AttemptOutputPath(key string, attemptID uuid.UUID) string {
	pos := strings.LastIndex(key, "/") + 1
	return key[:pos] + "attempts/" + attemptID.String() + "/" + key[pos:]
}

func RetryDelay(retries int) time.Duration {
	return time.Duration(min(5*(1<<min(max(retries-1, 0), 6)), 300)) * time.Second
}

func (s *Service) Dispatch(ctx context.Context, id uuid.UUID) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		job, err := jobstate.Lock(tx, id)
		if err != nil {
			return err
		}
		now := time.Now()
		if job.Status != models.JobPending || (job.NextRunAt != nil && job.NextRunAt.After(now)) {
			return nil
		}
		executor, err := s.executors.Resolve(job.Executor, job.Operation)
		if err != nil {
			return s.fail(tx, job, "executor_unavailable", err.Error())
		}
		attemptID := util.NewUuidV7()
		outputs := make([]taskqueue.Output, 0, len(job.Outputs))
		for _, output := range job.Outputs {
			outputs = append(outputs, taskqueue.Output{FileID: output.FileID.String(), Role: output.Role, Path: AttemptOutputPath(output.Path, attemptID)})
		}
		if len(outputs) == 0 && job.OutputPath != "" {
			fileID := ""
			if job.OutputFileID != nil {
				fileID = job.OutputFileID.String()
			}
			outputs = append(outputs, taskqueue.Output{FileID: fileID, Role: "result", Path: AttemptOutputPath(job.OutputPath, attemptID)})
		}
		if len(outputs) == 0 {
			return s.fail(tx, job, "invalid_request", "job has no output destinations")
		}
		var payload any
		if job.Operation == operation.Transcode && job.Executor == "compute" {
			params := job.Params
			if len(job.OperationParams) > 0 {
				if err := json.Unmarshal(job.OperationParams, &params); err != nil {
					return err
				}
			}
			inputFormat := ""
			if job.InputFileID != nil {
				var file models.File
				if err := tx.First(&file, "id = ? AND owner_id = ?", *job.InputFileID, job.OwnerID).Error; err != nil {
					return err
				}
				inputFormat = mimeToFormat(file.MimeType)
			}
			payload = taskqueue.ComputePayload{
				SchemaVersion: "1.0", JobID: job.ID.String(), AttemptID: attemptID.String(),
				InputPath: job.InputPath, InputFormat: inputFormat, OutputPath: outputs[0].Path,
				OutputFileID: outputs[0].FileID, TargetFormat: job.TargetFormat, Params: params,
				AttemptScopedCancel: true,
			}
		} else {
			inputs := make([]taskqueue.Input, 0, len(job.Inputs))
			for _, input := range job.Inputs {
				inputs = append(inputs, taskqueue.Input{FileID: input.FileID.String(), Role: input.Role, Path: input.Path})
			}
			payload = taskqueue.ExecutionPayload{
				SchemaVersion: "1.1", JobID: job.ID.String(), AttemptID: attemptID.String(),
				Generation: job.Generation, Operation: job.Operation, Executor: job.Executor,
				Params: job.OperationParams, Inputs: inputs, Outputs: outputs, TargetFormat: job.TargetFormat,
			}
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		manifest, err := json.Marshal(outputs)
		if err != nil {
			return err
		}
		values, err := json.Marshal(map[string]any{"payload": string(raw)})
		if err != nil {
			return err
		}
		attempt := models.JobAttempt{
			Base: models.Base{ID: attemptID}, JobID: job.ID, Generation: job.Generation,
			Executor: job.Executor, Status: "QUEUED", OutputManifest: operation.JSON(manifest),
			Stream: executor.Stream, DispatchPayload: operation.JSON(values),
		}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		if err := tx.Model(job).Updates(map[string]any{
			"status": models.JobProcessing, "attempt_id": attemptID,
			"lease_expires_at": now.Add(LeaseDuration), "next_run_at": nil,
		}).Error; err != nil {
			return err
		}
		if err := jobstate.OutputStatus(tx, job, "GENERATING"); err != nil {
			return err
		}
		if err := tx.Create(&models.OutboxEvent{
			Kind: "stream", Destination: executor.Stream, Payload: operation.JSON(values), AvailableAt: now,
		}).Error; err != nil {
			return err
		}
		return jobstate.Aggregate(tx, job.TaskID)
	})
}

func (s *Service) ProcessResult(ctx context.Context, payload taskqueue.ResultPayload) error {
	if (payload.SchemaVersion != "1.0" && payload.SchemaVersion != "1.1") ||
		(payload.Status != taskqueue.ResultStatusProcessing && payload.Status != taskqueue.ResultStatusCompleted && payload.Status != taskqueue.ResultStatusFailed) ||
		(payload.Progress != nil && (*payload.Progress < 0 || *payload.Progress > 100)) {
		return ErrInvalidResult
	}
	id, err := uuid.Parse(payload.JobID)
	if err != nil {
		return ErrInvalidResult
	}
	attemptID, err := uuid.Parse(payload.AttemptID)
	if err != nil {
		return ErrInvalidResult
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		job, err := jobstate.Lock(tx, id)
		if err != nil {
			return err
		}
		if job.Status != models.JobProcessing || job.AttemptID == nil || *job.AttemptID != attemptID {
			return nil
		}
		var attempt models.JobAttempt
		if err := tx.First(&attempt, "id = ? AND job_id = ?", attemptID, id).Error; err != nil {
			return err
		}
		now := time.Now()
		if payload.Status == taskqueue.ResultStatusProcessing {
			progress := job.Progress
			if payload.Progress != nil {
				progress = max(progress, *payload.Progress)
			}
			if err := tx.Model(&attempt).Update("status", "RUNNING").Error; err != nil {
				return err
			}
			return tx.Model(job).Updates(map[string]any{"progress": progress, "lease_expires_at": now.Add(LeaseDuration)}).Error
		}
		if payload.Status == taskqueue.ResultStatusCompleted {
			if err := publishArtifacts(tx, job, attempt, payload, now); err != nil {
				return err
			}
			if err := tx.Model(&attempt).Updates(map[string]any{"status": "COMPLETED", "finished_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Model(job).Updates(map[string]any{
				"status": models.JobCompleted, "progress": 100, "lease_expires_at": nil,
				"error_code": "", "error_message": "",
			}).Error; err != nil {
				return err
			}
			return jobstate.Aggregate(tx, job.TaskID)
		}
		if err := tx.Model(&attempt).Updates(map[string]any{
			"status": "FAILED", "finished_at": now, "error_code": payload.ErrorCode,
		}).Error; err != nil {
			return err
		}
		return s.retryOrFail(tx, job, payload.ErrorCode, payload.ErrorMessage,
			payload.Retryable != nil && *payload.Retryable, payload.RetryAfterSeconds, now)
	})
}

func publishArtifacts(tx *gorm.DB, job *models.Job, attempt models.JobAttempt, result taskqueue.ResultPayload, now time.Time) error {
	var expected []taskqueue.Output
	if err := json.Unmarshal(attempt.OutputManifest, &expected); err != nil {
		return err
	}
	artifacts := result.Artifacts
	if len(artifacts) == 0 && job.Operation == operation.Transcode && len(expected) == 1 {
		mime, _ := result.Metadata["mime_type"].(string)
		artifacts = []taskqueue.Artifact{{
			FileID: expected[0].FileID, Path: expected[0].Path, MimeType: mime,
			Size:  number(result.Metadata["output_size_bytes"]),
			Width: int(number(result.Metadata["width"])), Height: int(number(result.Metadata["height"])),
		}}
	}
	if len(artifacts) != len(expected) || len(expected) == 0 {
		return fmt.Errorf("%w: output count mismatch", ErrInvalidResult)
	}
	byID := make(map[string]taskqueue.Artifact, len(artifacts))
	for _, artifact := range artifacts {
		if _, duplicate := byID[artifact.FileID]; duplicate || artifact.Size <= 0 ||
			artifact.MimeType == "" || artifact.Width < 0 || artifact.Height < 0 {
			return fmt.Errorf("%w: invalid artifact metadata", ErrInvalidResult)
		}
		byID[artifact.FileID] = artifact
	}
	for i, output := range expected {
		artifact, ok := byID[output.FileID]
		if !ok || artifact.Path != output.Path {
			return fmt.Errorf("%w: artifact outside attempt manifest", ErrInvalidResult)
		}
		if output.FileID != "" {
			id, err := uuid.Parse(output.FileID)
			if err != nil {
				return err
			}
			res := tx.Model(&models.File{}).Where("id = ? AND owner_id = ?", id, job.OwnerID).
				Updates(map[string]any{"path": output.Path, "status": "UPLOADED", "size": artifact.Size,
					"mime_type": artifact.MimeType, "width": artifact.Width, "height": artifact.Height, "uploaded_at": now})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return fmt.Errorf("%w: output file missing", ErrInvalidResult)
			}
		}
		if i == 0 {
			if err := tx.Model(job).Update("output_path", output.Path).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func number(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		value, _ := n.Int64()
		return value
	default:
		return 0
	}
}

func (s *Service) fail(tx *gorm.DB, job *models.Job, code, message string) error {
	if err := tx.Model(job).Updates(map[string]any{
		"status": models.JobFailed, "error_code": code, "error_message": message, "lease_expires_at": nil,
	}).Error; err != nil {
		return err
	}
	if err := jobstate.OutputStatus(tx, job, "FAILED"); err != nil {
		return err
	}
	return jobstate.Aggregate(tx, job.TaskID)
}

func (s *Service) retryOrFail(tx *gorm.DB, job *models.Job, code, message string, retryable bool, retryAfter *int, now time.Time) error {
	// Never turn an ambiguous or already-submitted paid request into a new submission.
	var unresolved int64
	if err := tx.Model(&models.ProviderInvocation{}).
		Where("job_id = ? AND generation = ? AND phase IN ?", job.ID, job.Generation, []string{"SUBMITTING", "SUBMITTED", "UNKNOWN"}).
		Count(&unresolved).Error; err != nil {
		return err
	}
	if !retryable || unresolved > 0 || job.RetryCount >= job.MaxRetries {
		return s.fail(tx, job, code, message)
	}
	next := now.Add(RetryDelay(job.RetryCount + 1))
	if retryAfter != nil {
		next = maxTime(next, now.Add(time.Duration(min(max(*retryAfter, 0), 3600))*time.Second))
	}
	if err := tx.Model(job).Updates(map[string]any{
		"status": models.JobPending, "retry_count": job.RetryCount + 1, "progress": 0,
		"attempt_id": nil, "lease_expires_at": nil, "next_run_at": next,
		"error_code": code, "error_message": message,
	}).Error; err != nil {
		return err
	}
	if err := outbox.EnqueueJob(tx, job.ID, next); err != nil {
		return err
	}
	return jobstate.Aggregate(tx, job.TaskID)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// Recover rechecks the lease under the same lock used by results and cancellation.
func (s *Service) Recover(ctx context.Context, now time.Time) error {
	var candidates []uuid.UUID
	if err := s.db.WithContext(ctx).Model(&models.Job{}).
		Where("(status = ? AND updated_at < ? AND (next_run_at IS NULL OR next_run_at <= ?)) OR (status = ? AND (lease_expires_at <= ? OR (lease_expires_at IS NULL AND updated_at < ?)))",
			models.JobPending, now.Add(-2*time.Minute), now, models.JobProcessing, now, now.Add(-LeaseDuration)).
		Order("updated_at").Limit(100).Pluck("id", &candidates).Error; err != nil {
		return err
	}
	for _, id := range candidates {
		if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			job, err := jobstate.Lock(tx, id)
			if err != nil {
				return err
			}
			if job.Status == models.JobPending {
				if job.NextRunAt != nil && job.NextRunAt.After(now) {
					return nil
				}
				if err := outbox.EnqueueJob(tx, job.ID, now); err != nil {
					return err
				}
				return tx.Model(job).Update("updated_at", now).Error
			}
			if job.Status != models.JobProcessing || (job.LeaseExpiresAt != nil && job.LeaseExpiresAt.After(now)) ||
				(job.LeaseExpiresAt == nil && job.UpdatedAt.After(now.Add(-LeaseDuration))) {
				return nil
			}
			if job.AttemptID != nil {
				var attempt models.JobAttempt
				err := tx.First(&attempt, "id = ?", *job.AttemptID).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if err == nil && attempt.Status == "QUEUED" {
					// No start receipt: redeliver the same attempt instead of exhausting retries in a backlog.
					if err := tx.Create(&models.OutboxEvent{Kind: "stream", Destination: attempt.Stream,
						Payload: attempt.DispatchPayload, AvailableAt: now}).Error; err != nil {
						return err
					}
					return tx.Model(job).Update("lease_expires_at", now.Add(LeaseDuration)).Error
				}
				if err == nil {
					if err := tx.Model(&attempt).Updates(map[string]any{"status": "EXPIRED", "finished_at": now}).Error; err != nil {
						return err
					}
				}
				if err := outbox.CancelAttempt(tx, *job.AttemptID); err != nil {
					return err
				}
			}
			return s.retryOrFail(tx, job, "execution_timeout", "execution lease expired", true, nil, now)
		}); err != nil {
			return err
		}
	}
	return nil
}

func mimeToFormat(mime string) string {
	if mime == "image/jpg" {
		return "jpeg"
	}
	format := strings.TrimPrefix(mime, "image/")
	switch format {
	case "jpeg", "png", "webp", "gif", "avif", "jxl", "heic":
		return format
	case "heif":
		return "heic"
	default:
		return ""
	}
}
