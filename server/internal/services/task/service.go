package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

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
			Status:             models.TaskPending,
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
				Status:       models.JobPending,
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
		if err := task.EnqueueJob(ctx, s.kv, jid); err != nil {
			slog.Error("failed to enqueue job during task creation, will be recovered by sweeper", "job_id", jid, "error", err)
		}
	}

	return s.GetTask(ctx, input.OwnerID, createdTaskID)
}

func (s *Service) GetTask(ctx context.Context, ownerID uuid.UUID, id uuid.UUID) (*models.Task, error) {
	var t models.Task
	if err := s.db.WithContext(ctx).Preload("Jobs.InputFile").Preload("Jobs.OutputFile").Preload("Jobs").First(&t, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
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

type StatsResponse struct {
	Pending         int     `json:"PENDING"`
	Processing      int     `json:"PROCESSING"`
	Completed       int     `json:"COMPLETED"`
	Failed          int     `json:"FAILED"`
	PartiallyFailed int     `json:"PARTIALLY_FAILED"`
	StorageUsed     int64   `json:"storage_used"`
	StorageLimit    int64   `json:"storage_limit"`
	ActiveWorkers   int     `json:"active_workers"`
	DailyChart      []int   `json:"daily_chart"`
	GrowthRate      float64 `json:"growth_rate"`
}

// GetStats gets the stats for the given owner.
//
// It returns a StatsResponse, and an error if any.
func (s *Service) GetStats(ctx context.Context, ownerID uuid.UUID) (*StatsResponse, error) {
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

	stats := &StatsResponse{
		Pending:         0,
		Processing:      0,
		Completed:       0,
		Failed:          0,
		PartiallyFailed: 0,
		StorageLimit:    10995116277760, // 10 TB in bytes
	}

	for _, r := range results {
		switch r.Status {
		case "PENDING":
			stats.Pending = r.Count
		case "PROCESSING":
			stats.Processing = r.Count
		case "COMPLETED":
			stats.Completed = r.Count
		case "FAILED":
			stats.Failed = r.Count
		case "PARTIALLY_FAILED":
			stats.PartiallyFailed = r.Count
		}
	}

	// 2. Query total storage used by the owner's uploaded files
	var storageUsed int64
	err = s.db.WithContext(ctx).Model(&models.File{}).
		Select("COALESCE(SUM(size), 0)").
		Where("owner_id = ? AND status = ?", ownerID, "UPLOADED").
		Row().Scan(&storageUsed)
	if err != nil {
		slog.Error("failed to query storage used", "owner_id", ownerID, "error", err)
	}
	stats.StorageUsed = storageUsed

	// 3. Query active workers from Redis stream consumer group
	activeWorkers := 0
	consumers, err := s.kv.XInfoConsumers(ctx, "transcoder:compute:stream", "transcoder:compute:group").Result()
	if err == nil {
		for _, c := range consumers {
			// If active within 5 minutes (300,000 ms)
			if c.Idle < 300000 {
				activeWorkers++
			}
		}
	} else {
		slog.Debug("failed to query active workers from Redis, group might not exist yet", "error", err)
	}
	stats.ActiveWorkers = activeWorkers

	// 4. Query last 7 days daily counts
	var dailyCounts []struct {
		Date  time.Time `gorm:"column:date"`
		Count int       `gorm:"column:count"`
	}
	err = s.db.WithContext(ctx).Model(&models.Task{}).
		Select("date_trunc('day', created_at) as date, count(id) as count").
		Where("owner_id = ? AND created_at >= now() - interval '7 days'", ownerID).
		Group("date_trunc('day', created_at)").
		Order("date_trunc('day', created_at) ASC").
		Scan(&dailyCounts).Error
	if err != nil {
		slog.Error("failed to query daily task volume", "owner_id", ownerID, "error", err)
	}

	dailyMap := make(map[string]int)
	for _, dc := range dailyCounts {
		dateStr := dc.Date.Format("2006-01-02")
		dailyMap[dateStr] = dc.Count
	}

	dailyChart := make([]int, 7)
	now := time.Now()
	for i := 0; i < 7; i++ {
		t := now.AddDate(0, 0, -6+i)
		dateStr := t.Format("2006-01-02")
		dailyChart[i] = dailyMap[dateStr]
	}
	stats.DailyChart = dailyChart

	// 5. Query growth rate (this week vs last week)
	var countThisWeek int64
	var countPrevWeek int64

	s.db.WithContext(ctx).Model(&models.Task{}).
		Where("owner_id = ? AND created_at >= now() - interval '7 days'", ownerID).
		Count(&countThisWeek)

	s.db.WithContext(ctx).Model(&models.Task{}).
		Where("owner_id = ? AND created_at >= now() - interval '14 days' AND created_at < now() - interval '7 days'", ownerID).
		Count(&countPrevWeek)

	if countPrevWeek > 0 {
		stats.GrowthRate = float64(countThisWeek-countPrevWeek) / float64(countPrevWeek) * 100.0
	} else if countThisWeek > 0 {
		stats.GrowthRate = 100.0
	} else {
		stats.GrowthRate = 0.0
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
