package job

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"transcoder/server/internal/models"
	"transcoder/server/internal/task"
	"transcoder/server/internal/transcode"

	"gorm.io/gorm"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrInputPathRequired    = errors.New("inputPath is required")
	ErrOutputPathRequired   = errors.New("outputPath is required")
	ErrTargetFormatRequired = errors.New("targetFormat is required")
)

// QueueEnqueueError indicates that the job record was created successfully
// but the queue handoff failed.
type QueueEnqueueError struct {
	JobID uuid.UUID
	Err   error
}

func (e *QueueEnqueueError) Error() string {
	return fmt.Sprintf("enqueue job %s: %v", e.JobID, e.Err)
}

func (e *QueueEnqueueError) Unwrap() error {
	return e.Err
}

// CreateJobInput is the application-level input for creating a job.
type CreateJobInput struct {
	OwnerID      uuid.UUID
	InputPath    string
	OutputPath   string
	TargetFormat string
	Params       transcode.Params
}

// Service orchestrates job persistence and queue handoff.
type Service struct {
	db *gorm.DB
	kv *redis.Client
}

func NewService(db *gorm.DB, kv *redis.Client) *Service {
	return &Service{
		db: db,
		kv: kv,
	}
}

// CreateJob validates input, persists a pending job, and attempts to enqueue it.
// It may return a non-nil job together with an error when the database write
// succeeds but queue submission fails.
func (s *Service) CreateJob(ctx context.Context, input CreateJobInput) (*models.Job, error) {
	normalized, err := normalizeCreateJobInput(input)
	if err != nil {
		return nil, err
	}

	if err := normalized.Params.Validate(normalized.TargetFormat); err != nil {
		return nil, fmt.Errorf("validate transcode params: %w", err)
	}

	jobModel := &models.Job{
		OwnerID:      normalized.OwnerID,
		InputPath:    normalized.InputPath,
		OutputPath:   normalized.OutputPath,
		TargetFormat: normalized.TargetFormat,
		Status:       models.JobPending,
		Params:       normalized.Params,
	}

	if err := s.db.WithContext(ctx).Create(jobModel).Error; err != nil {
		return nil, fmt.Errorf("create job record: %w", err)
	}

	if err := task.EnqueueJob(ctx, s.kv, jobModel.ID.String()); err != nil {
		return jobModel, &QueueEnqueueError{
			JobID: jobModel.ID,
			Err:   err,
		}
	}

	return jobModel, nil
}

// GetJob returns a single job by its ID.
func (s *Service) GetJob(ctx context.Context, id uuid.UUID) (*models.Job, error) {
	var j models.Job
	if err := s.db.WithContext(ctx).First(&j, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &j, nil
}

func normalizeCreateJobInput(input CreateJobInput) (CreateJobInput, error) {
	input.InputPath = strings.TrimSpace(input.InputPath)
	input.OutputPath = strings.TrimSpace(input.OutputPath)
	input.TargetFormat = strings.TrimSpace(input.TargetFormat)

	switch {
	case input.InputPath == "":
		return CreateJobInput{}, ErrInputPathRequired
	case input.OutputPath == "":
		return CreateJobInput{}, ErrOutputPathRequired
	case input.TargetFormat == "":
		return CreateJobInput{}, ErrTargetFormatRequired
	default:
		return input, nil
	}
}
