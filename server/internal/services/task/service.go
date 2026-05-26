package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"transcoder/server/internal/models"
	"transcoder/server/internal/task"
	"transcoder/server/internal/transcode"
	"transcoder/server/pkg/util"

	"gorm.io/gorm"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrNoItemsProvided     = errors.New("at least one item is required to create a task")
	ErrWorkflowNotFound    = errors.New("workflow not found")
	ErrInvalidFileID       = errors.New("file_id is required and must not be nil")
	ErrFileNotFound        = errors.New("file not found or not uploaded")
	ErrIdempotencyConflict = errors.New("idempotency key conflict: request payload mismatch")
)

type CreateTaskInput struct {
	OwnerID        uuid.UUID
	WorkflowID     *uuid.UUID
	Items          []TaskItemInput
	TargetFormat   string
	Params         transcode.Params
	IdempotencyKey *string
}

type TaskItemInput struct {
	FileID uuid.UUID `json:"file_id"`
}

// Fingerprint calculates a SHA-256 hash representing the task creation request details.
func (input *CreateTaskInput) Fingerprint() string {
	var sb strings.Builder
	sb.WriteString(input.TargetFormat)
	sb.WriteString(":")
	for _, item := range input.Items {
		sb.WriteString(item.FileID.String())
		sb.WriteString(",")
	}
	sb.WriteString(fmt.Sprintf(":%s", input.Params.Engine))
	sb.WriteString(fmt.Sprintf(":%v", input.Params.EngineParams))
	hash := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(hash[:])
}

type Service struct {
	db *gorm.DB
	kv *redis.Client
}

func NewService(db *gorm.DB, kv *redis.Client) *Service {
	return &Service{db: db, kv: kv}
}

func (s *Service) CreateTask(ctx context.Context, input CreateTaskInput) (*models.Task, error) {
	if len(input.Items) == 0 {
		return nil, ErrNoItemsProvided
	}

	var idempotencyStr string
	if input.IdempotencyKey != nil {
		idempotencyStr = *input.IdempotencyKey
	}

	fingerprint := input.Fingerprint()

	if idempotencyStr != "" {
		var existing models.Task
		if err := s.db.WithContext(ctx).Preload("Jobs").Where("idempotency_key = ? AND owner_id = ?", idempotencyStr, input.OwnerID).First(&existing).Error; err == nil {
			if existing.RequestFingerprint != fingerprint {
				return nil, ErrIdempotencyConflict
			}
			return &existing, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("check idempotency: %w", err)
		}
	}

	var targetFormat string
	var params transcode.Params

	if input.WorkflowID != nil {
		var wf models.Workflow
		if err := s.db.WithContext(ctx).First(&wf, "id = ?", *input.WorkflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrWorkflowNotFound
			}
			return nil, fmt.Errorf("fetch workflow: %w", err)
		}
		targetFormat = strings.ToUpper(strings.TrimSpace(wf.TargetFormat))
		params = wf.Params
	} else {
		targetFormat = strings.ToUpper(strings.TrimSpace(input.TargetFormat))
		params = input.Params
	}

	if targetFormat == "" {
		return nil, errors.New("targetFormat is required if workflow is not specified")
	}

	if err := params.Validate(targetFormat); err != nil {
		return nil, fmt.Errorf("validate transcode params: %w", err)
	}

	var createdTaskID uuid.UUID
	var createdJobIDs []string

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		t := &models.Task{
			OwnerID:            input.OwnerID,
			Status:             "PENDING",
			WorkflowID:         input.WorkflowID,
			IdempotencyKey:     input.IdempotencyKey,
			RequestFingerprint: fingerprint,
		}

		if err := tx.Create(t).Error; err != nil {
			return fmt.Errorf("create task: %w", err)
		}
		createdTaskID = t.ID

		var jobs []models.Job
		for _, item := range input.Items {
			if item.FileID == uuid.Nil {
				return ErrInvalidFileID
			}

			var inputFile models.File
			if err := tx.Where("id = ? AND owner_id = ? AND status = ?", item.FileID, input.OwnerID, "UPLOADED").First(&inputFile).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrFileNotFound
				}
				return fmt.Errorf("find input file %q (must be UPLOADED): %w", item.FileID, err)
			}

			outputFileID := util.NewUuidV7()
			outputPath := fmt.Sprintf("outputs/%s/%s.%s", input.OwnerID.String(), outputFileID.String(), strings.ToLower(targetFormat))
			outputName := strings.TrimSuffix(inputFile.Name, filepath.Ext(inputFile.Name)) + "." + strings.ToLower(targetFormat)

			outputFile := models.File{
				Base: models.Base{
					ID: outputFileID,
				},
				OwnerID: input.OwnerID,
				Name:    outputName,
				Path:    outputPath,
				Status:  "GENERATING",
			}
			if err := tx.Create(&outputFile).Error; err != nil {
				return fmt.Errorf("create output file: %w", err)
			}

			jobModel := models.Job{
				OwnerID:      input.OwnerID,
				TaskID:       &t.ID,
				InputFileID:  &inputFile.ID,
				OutputFileID: &outputFile.ID,
				InputPath:    inputFile.Path,
				OutputPath:   outputPath,
				TargetFormat: targetFormat,
				Status:       "PENDING",
				WorkflowID:   input.WorkflowID,
				Params:       params,
			}
			jobs = append(jobs, jobModel)
		}

		if err := tx.Create(&jobs).Error; err != nil {
			return fmt.Errorf("create jobs: %w", err)
		}

		for _, j := range jobs {
			createdJobIDs = append(createdJobIDs, j.ID.String())
		}
		return nil
	})

	if err != nil {
		if idempotencyStr != "" && isUniqueConstraintViolation(err) {
			var existing models.Task
			if errSelect := s.db.WithContext(ctx).Preload("Jobs").Where("idempotency_key = ? AND owner_id = ?", idempotencyStr, input.OwnerID).First(&existing).Error; errSelect == nil {
				if existing.RequestFingerprint != fingerprint {
					return nil, ErrIdempotencyConflict
				}
				return &existing, nil
			}
		}
		return nil, err
	}

	for _, jid := range createdJobIDs {
		_ = task.EnqueueJob(ctx, s.kv, jid)
	}

	return s.GetTask(ctx, createdTaskID)
}

func (s *Service) GetTask(ctx context.Context, id uuid.UUID) (*models.Task, error) {
	var t models.Task
	if err := s.db.WithContext(ctx).Preload("Jobs").First(&t, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

type ListTasksParams struct {
	Count  int
	Page   int
	Status string
}

// ListTasks lists tasks for the given owner.
//
// It returns the tasks, the total count of tasks, and an error if any.
func (s *Service) ListTasks(ctx context.Context, ownerID uuid.UUID, params ListTasksParams) ([]models.Task, int64, error) {
	var tasks []models.Task
	var total int64

	query := s.db.WithContext(ctx).Debug().Model(&models.Task{}).Where("owner_id = ?", ownerID)

	if params.Status != "" {
		query = query.Where("status = ?", params.Status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var limit int = 20
	var offset int = 0

	if params.Count > 0 && params.Count <= 100 {
		limit = params.Count
	}
	if params.Page > 0 {
		offset = (params.Page - 1) * limit
	}

	if err := query.Preload("Jobs").Order("created_at DESC").Limit(limit).Offset(offset).Find(&tasks).Error; err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

// GetStats gets the stats for the given owner.
//
// It returns a map of status to count, and an error if any.
func (s *Service) GetStats(ctx context.Context, ownerID uuid.UUID) (map[string]int, error) {
	var results []struct {
		Status string `gorm:"column:status"`
		Count  int    `gorm:"column:count"`
	}

	err := s.db.WithContext(ctx).Model(&models.Task{}).
		Select("status, count(id) as count").
		Where("owner_id = ?", ownerID).
		Group("status").
		Scan(&results).Error
	if err != nil {
		return nil, err
	}

	stats := map[string]int{
		"PENDING":    0,
		"PROCESSING": 0,
		"COMPLETED":  0,
		"FAILED":     0,
	}

	for _, r := range results {
		stats[r.Status] = r.Count
	}

	return stats, nil
}

func isUniqueConstraintViolation(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "23505") || strings.Contains(errStr, "duplicate key") || strings.Contains(errStr, "unique constraint")
}
