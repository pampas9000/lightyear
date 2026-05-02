package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"transcoder/server/internal/models"
	"transcoder/server/internal/task"

	"gorm.io/gorm"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrNoItemsProvided  = errors.New("at least one item is required to create a task")
	ErrWorkflowNotFound = errors.New("workflow not found")
)

type CreateTaskInput struct {
	OwnerID    uuid.UUID
	WorkflowID *uuid.UUID
	Items      []TaskItemInput

	TargetFormat string
	Params       map[string]any
}

type TaskItemInput struct {
	InputPath  string
	OutputPath string
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

	var targetFormat string
	var params map[string]any

	if input.WorkflowID != nil {
		var wf models.Workflow
		if err := s.db.WithContext(ctx).First(&wf, "id = ?", *input.WorkflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrWorkflowNotFound
			}
			return nil, fmt.Errorf("fetch workflow: %w", err)
		}
		targetFormat = wf.TargetFormat
		if wf.Params != "" {
			json.Unmarshal([]byte(wf.Params), &params)
		}
	} else {
		targetFormat = input.TargetFormat
		params = input.Params
	}

	if targetFormat == "" {
		return nil, errors.New("targetFormat is required if workflow is not specified")
	}

	var createdTaskID uuid.UUID
	var createdJobIDs []string

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		t := &models.Task{
			OwnerID:    input.OwnerID,
			Status:     "PENDING",
			WorkflowID: input.WorkflowID,
		}

		if err := tx.Create(t).Error; err != nil {
			return fmt.Errorf("create task: %w", err)
		}
		createdTaskID = t.ID

		var jobs []models.Job
		for _, item := range input.Items {
			var inputFile models.File
			if err := tx.Where("path = ? AND owner_id = ?", item.InputPath, input.OwnerID).First(&inputFile).Error; err != nil {
				return fmt.Errorf("find input file %q: %w", item.InputPath, err)
			}

			outputFile := models.File{
				OwnerID: input.OwnerID,
				Name:    filepath.Base(item.OutputPath),
				Path:    item.OutputPath,
			}
			if err := tx.Create(&outputFile).Error; err != nil {
				return fmt.Errorf("create output file: %w", err)
			}

			jobModel := models.Job{
				OwnerID:      input.OwnerID,
				TaskID:       &t.ID,
				InputFileID:  &inputFile.ID,
				OutputFileID: &outputFile.ID,
				InputPath:    item.InputPath,
				OutputPath:   item.OutputPath,
				TargetFormat: targetFormat,
				Status:       "PENDING",
				WorkflowID:   input.WorkflowID,
			}
			if params != nil {
				b, _ := json.Marshal(params)
				jobModel.Params = string(b)
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

func (s *Service) ListTasks(ctx context.Context, ownerID uuid.UUID) ([]models.Task, error) {
	var tasks []models.Task
	if err := s.db.WithContext(ctx).Preload("Jobs").Where("owner_id = ?", ownerID).Order("created_at desc").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

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
