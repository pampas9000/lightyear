package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/outbox"
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

// CreateJob atomically commits a pending legacy job and its outbox wakeup.
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

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(jobModel).Error; err != nil {
			return err
		}
		return outbox.EnqueueJob(tx, jobModel.ID, time.Now())
	}); err != nil {
		return nil, fmt.Errorf("create job and outbox: %w", err)
	}

	return jobModel, nil
}

// GetJob returns a single job belonging to the authenticated owner.
func (s *Service) GetJob(ctx context.Context, ownerID, id uuid.UUID) (*models.Job, error) {
	var j models.Job
	if err := s.db.WithContext(ctx).
		Preload("Inputs", func(db *gorm.DB) *gorm.DB { return db.Order("position") }).
		Preload("Outputs", func(db *gorm.DB) *gorm.DB { return db.Order("position") }).
		Preload("Inputs.File").Preload("Outputs.File").
		First(&j, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
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
