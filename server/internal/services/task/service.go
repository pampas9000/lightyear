package task

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"transcoder/server/internal/execution"
	"transcoder/server/internal/models"
	"transcoder/server/internal/operation"
	"transcoder/server/internal/services/jobstate"
	"transcoder/server/internal/services/outbox"
	"transcoder/server/internal/transcode"
	"transcoder/server/pkg/util"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

var (
	ErrNoItemsProvided     = errors.New("at least one item is required to create a task")
	ErrWorkflowNotFound    = errors.New("workflow not found")
	ErrInvalidFileID       = errors.New("file_id is required and must not be nil")
	ErrFileNotFound        = errors.New("file not found or not uploaded")
	ErrIdempotencyConflict = errors.New("idempotency key conflict: request payload mismatch")
	ErrTaskNotFound        = errors.New("task not found")
	ErrNoJobsToRetry       = errors.New("no failed jobs available to retry")
	ErrJobNotRetryable     = errors.New("active jobs cannot be retried")
	ErrInvalidJobID        = errors.New("job does not belong to task")
)

type CreateTaskInput struct {
	OwnerID        uuid.UUID
	WorkflowID     *uuid.UUID
	Items          []TaskItemInput
	TargetFormat   string
	Params         transcode.Params
	IdempotencyKey *string
	Jobs           []JobSpec
}

type TaskItemInput struct {
	FileID uuid.UUID `json:"file_id"`
}

// Fingerprint calculates a SHA-256 hash representing the resolved task creation
// request details. It must be computed after the workflow (if any) is resolved so
// that the full effective transcoding configuration — engine, color pipeline,
// profile policy and strict mode — participates in idempotency matching.
func (input *CreateTaskInput) Fingerprint(targetFormat string, params transcode.Params) string {
	ids := make([]string, 0, len(input.Items))
	for _, item := range input.Items {
		ids = append(ids, item.FileID.String())
	}
	slices.Sort(ids)

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		paramsJSON = []byte(fmt.Sprintf("%+v", params))
	}

	var sb strings.Builder
	sb.WriteString(targetFormat)
	sb.WriteString(":")
	sb.WriteString(strings.Join(ids, ","))
	if input.WorkflowID != nil {
		sb.WriteString(fmt.Sprintf(":workflow=%s", input.WorkflowID.String()))
	}
	sb.WriteString(fmt.Sprintf(":params=%s", paramsJSON))

	hash := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(hash[:])
}

type Service struct {
	db        *gorm.DB
	kv        *redis.Client
	executors execution.Registry
}

func NewService(db *gorm.DB, kv *redis.Client) *Service {
	return NewServiceWithExecutors(db, kv, execution.DefaultRegistry())
}

func NewServiceWithExecutors(db *gorm.DB, kv *redis.Client, executors execution.Registry) *Service {
	return &Service{db: db, kv: kv, executors: executors}
}

func (s *Service) CreateTask(ctx context.Context, input CreateTaskInput) (*models.Task, error) {
	if len(input.Items) == 0 && len(input.Jobs) == 0 {
		return nil, ErrNoItemsProvided
	}
	if len(input.Items)+len(input.Jobs) > 100 {
		return nil, fmt.Errorf("%w: at most 100 jobs are allowed", operation.ErrInvalid)
	}
	if len(input.Jobs) > 0 && (len(input.Items) > 0 || input.WorkflowID != nil || input.TargetFormat != "" || input.Params.Engine != "" || len(input.Params.EngineParams) > 0 || input.Params.Color != nil || input.Params.ProfilePolicy != nil || input.Params.Strict != nil || input.Params.Strategy != nil) {
		return nil, fmt.Errorf("%w: jobs cannot be combined with legacy task parameters", operation.ErrInvalid)
	}

	var idempotencyStr string
	if input.IdempotencyKey != nil {
		idempotencyStr = strings.TrimSpace(*input.IdempotencyKey)
		if len(idempotencyStr) > 255 {
			return nil, fmt.Errorf("%w: idempotency_key is too long", operation.ErrInvalid)
		}
		input.IdempotencyKey = nil
		if idempotencyStr != "" {
			input.IdempotencyKey = new(idempotencyStr)
		}
	}

	var targetFormat string
	var params transcode.Params

	if input.WorkflowID != nil {
		var wf models.Workflow
		if err := s.db.WithContext(ctx).First(&wf, "id = ? AND owner_id = ?", *input.WorkflowID, input.OwnerID).Error; err != nil {
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

	specs := slices.Clone(input.Jobs)
	if len(specs) == 0 {
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", operation.ErrInvalid, err)
		}
		for _, item := range input.Items {
			specs = append(specs, JobSpec{
				Operation: operation.Transcode, Executor: "compute",
				Inputs:       []InputSpec{{FileID: item.FileID, Role: "source"}},
				TargetFormat: targetFormat, Params: operation.JSON(paramsJSON),
			})
		}
	}
	for i, spec := range specs {
		resolved, err := normalizeSpec(spec)
		if err != nil {
			return nil, err
		}
		if _, err := s.executors.Resolve(resolved.Executor, resolved.Operation); err != nil {
			return nil, err
		}
		specs[i] = resolved
	}
	raw, err := encodeSpecs(specs)
	if err != nil {
		return nil, err
	}
	fingerprintInput, err := json.Marshal(struct {
		WorkflowID *uuid.UUID      `json:"workflow_id,omitempty"`
		Jobs       json.RawMessage `json:"jobs"`
	}{input.WorkflowID, raw})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(fingerprintInput)
	fingerprint := hex.EncodeToString(hash[:])
	matchesFingerprint := func(existing string) bool {
		return existing == fingerprint || (len(input.Jobs) == 0 && existing == input.Fingerprint(targetFormat, params))
	}

	if idempotencyStr != "" {
		var existing models.Task
		if err := s.db.WithContext(ctx).Preload("Jobs").Where("idempotency_key = ? AND owner_id = ?", idempotencyStr, input.OwnerID).First(&existing).Error; err == nil {
			if !matchesFingerprint(existing.RequestFingerprint) {
				return nil, ErrIdempotencyConflict
			}
			return &existing, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("check idempotency: %w", err)
		}
	}

	var createdTaskID uuid.UUID
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Extract and deduplicate file IDs from input items
		fileIDsMap := make(map[uuid.UUID]bool)
		for _, spec := range specs {
			for _, item := range spec.Inputs {
				fileIDsMap[item.FileID] = true
			}
		}

		fileIDs := make([]uuid.UUID, 0, len(fileIDsMap))
		for id := range fileIDsMap {
			fileIDs = append(fileIDs, id)
		}

		// 2. Sort file IDs to prevent database deadlocks
		slices.SortFunc(fileIDs, func(a, b uuid.UUID) int {
			return cmp.Compare(a.String(), b.String())
		})

		// Cleanup and submission both lock drafts before files, in UUID order.
		if len(fileIDs) > 0 {
			var drafts []models.DraftFile
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("file_id IN ? AND owner_id = ?", fileIDs, input.OwnerID).Order("file_id").Find(&drafts).Error; err != nil {
				return err
			}
		}
		fileLockMap := make(map[uuid.UUID]models.File)
		for _, id := range fileIDs {
			var f models.File
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ? AND status = ?", id, input.OwnerID, "UPLOADED").First(&f).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrFileNotFound
				}
				return fmt.Errorf("lock input file %q: %w", id, err)
			}
			fileLockMap[id] = f
		}

		// Delete corresponding draft_files records inside the transaction
		if len(fileIDs) > 0 {
			if err := tx.Where("file_id IN ? AND owner_id = ?", fileIDs, input.OwnerID).Delete(&models.DraftFile{}).Error; err != nil {
				return fmt.Errorf("delete draft files: %w", err)
			}
		}

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

		for _, spec := range specs {
			jobModel := models.Job{
				OwnerID:   input.OwnerID,
				TaskID:    &t.ID,
				Operation: spec.Operation, Executor: spec.Executor, OperationParams: spec.Params,
				TargetFormat: spec.TargetFormat,
				Status:       models.JobPending,
				WorkflowID:   input.WorkflowID,
			}
			if spec.Operation == operation.Transcode {
				if err := json.Unmarshal(spec.Params, &jobModel.Params); err != nil {
					return err
				}
			}
			name := "generated"
			for i, item := range spec.Inputs {
				file := fileLockMap[item.FileID]
				jobModel.Inputs = append(jobModel.Inputs, models.JobInput{
					FileID: file.ID, Role: item.Role, Position: i, Path: file.Path,
				})
				if item.Role == "source" {
					jobModel.InputFileID = new(file.ID)
					jobModel.InputPath = file.Path
					name = strings.TrimSuffix(file.Name, filepath.Ext(file.Name))
				}
			}
			for i := range spec.outputCount {
				fileID := util.NewUuidV7()
				ext := strings.ToLower(spec.TargetFormat)
				outputPath := fmt.Sprintf("outputs/%s/%s.%s", input.OwnerID, fileID, ext)
				file := models.File{Base: models.Base{ID: fileID}, OwnerID: input.OwnerID,
					Name: fmt.Sprintf("%s-%d.%s", name, i+1, ext), Path: outputPath, Status: "GENERATING"}
				if spec.outputCount == 1 {
					file.Name = name + "." + ext
				}
				if err := tx.Create(&file).Error; err != nil {
					return err
				}
				jobModel.Outputs = append(jobModel.Outputs, models.JobOutput{FileID: fileID, Role: "result", Position: i, Path: outputPath})
				if i == 0 {
					jobModel.OutputFileID, jobModel.OutputPath = new(fileID), outputPath
				}
			}
			if err := tx.Create(&jobModel).Error; err != nil {
				return fmt.Errorf("create job: %w", err)
			}
			if err := outbox.EnqueueJob(tx, jobModel.ID, time.Now()); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		if idempotencyStr != "" && isUniqueConstraintViolation(err) {
			var existing models.Task
			if errSelect := s.db.WithContext(ctx).Preload("Jobs").Where("idempotency_key = ? AND owner_id = ?", idempotencyStr, input.OwnerID).First(&existing).Error; errSelect == nil {
				if !matchesFingerprint(existing.RequestFingerprint) {
					return nil, ErrIdempotencyConflict
				}
				return &existing, nil
			}
		}
		return nil, err
	}

	return s.GetTask(ctx, input.OwnerID, createdTaskID)
}

func (s *Service) GetTask(ctx context.Context, ownerID uuid.UUID, id uuid.UUID) (*models.Task, error) {
	var t models.Task
	if err := s.db.WithContext(ctx).Preload("Jobs.InputFile").Preload("Jobs.OutputFile").
		Preload("Jobs.Inputs", func(db *gorm.DB) *gorm.DB { return db.Order("position") }).
		Preload("Jobs.Outputs", func(db *gorm.DB) *gorm.DB { return db.Order("position") }).
		Preload("Jobs.Inputs.File").Preload("Jobs.Outputs.File").
		Preload("Jobs", func(db *gorm.DB) *gorm.DB { return db.Order("id") }).
		First(&t, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

type RetryTaskInput struct {
	OwnerID         uuid.UUID
	TaskID          uuid.UUID
	TargetFormat    string
	Params          *transcode.Params
	JobIDs          []uuid.UUID
	RetryAll        bool
	OperationParams *operation.JSON
}

func (s *Service) RetryTask(ctx context.Context, input RetryTaskInput) (*models.Task, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parent models.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&parent, "id = ? AND owner_id = ?", input.TaskID, input.OwnerID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTaskNotFound
			}
			return err
		}
		var jobs []models.Job
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Outputs.File").
			Where("task_id = ?", parent.ID).Order("id").Find(&jobs).Error; err != nil {
			return err
		}
		for _, id := range input.JobIDs {
			if !slices.ContainsFunc(jobs, func(j models.Job) bool { return j.ID == id }) {
				return ErrInvalidJobID
			}
		}
		selected := 0
		for _, j := range jobs {
			explicit := slices.Contains(input.JobIDs, j.ID)
			if len(input.JobIDs) > 0 && !explicit {
				continue
			}
			if !explicit && !input.RetryAll && j.Status != models.JobFailed {
				continue
			}
			if !jobstate.Terminal(j.Status) {
				if explicit {
					return ErrJobNotRetryable
				}
				continue
			}
			var unresolved int64
			if err := tx.Model(&models.ProviderInvocation{}).
				Where("job_id = ? AND generation = ? AND phase IN ?", j.ID, j.Generation, []string{"SUBMITTING", "SUBMITTED", "UNKNOWN"}).
				Count(&unresolved).Error; err != nil {
				return err
			}
			if unresolved > 0 {
				return fmt.Errorf("%w: reconcile external invocation before retry", ErrJobNotRetryable)
			}
			if _, err := s.executors.Resolve(j.Executor, j.Operation); err != nil {
				return err
			}
			format := cmp.Or(strings.ToUpper(strings.TrimSpace(input.TargetFormat)), j.TargetFormat)
			if format == "JPG" {
				format = "JPEG"
			}
			if !slices.Contains([]string{"AVIF", "JXL", "HEIC", "JPEG", "PNG", "WEBP"}, format) {
				return fmt.Errorf("%w: unsupported target_format", operation.ErrInvalid)
			}
			raw := j.OperationParams
			if len(raw) == 0 {
				encoded, err := json.Marshal(j.Params)
				if err != nil {
					return err
				}
				raw = operation.JSON(encoded)
			}
			if input.Params != nil {
				if j.Operation != operation.Transcode || input.OperationParams != nil {
					return fmt.Errorf("%w: params are only for transcode", operation.ErrInvalid)
				}
				encoded, err := json.Marshal(input.Params)
				if err != nil {
					return err
				}
				raw = operation.JSON(encoded)
			}
			if input.OperationParams != nil {
				raw = *input.OperationParams
			}
			normalized, count, err := operation.Normalize(j.Operation, raw, format)
			if err != nil {
				return err
			}
			if count != len(j.Outputs) && len(j.Outputs) > 0 {
				return fmt.Errorf("%w: retry cannot change output count; create a new task", operation.ErrInvalid)
			}
			updates := map[string]any{"target_format": format, "operation_params": normalized,
				"status": models.JobPending, "progress": 0, "error_message": "", "error_code": "",
				"retry_count": 0, "attempt_id": nil, "lease_expires_at": nil,
				"generation": j.Generation + 1, "next_run_at": time.Now()}
			if j.Operation == operation.Transcode {
				var p transcode.Params
				if err := json.Unmarshal(normalized, &p); err != nil {
					return err
				}
				updates["params"] = p
			}
			for i, output := range j.Outputs {
				key := strings.TrimSuffix(output.Path, filepath.Ext(output.Path)) + "." + strings.ToLower(format)
				if err := tx.Model(&output).Update("path", key).Error; err != nil {
					return err
				}
				name := strings.TrimSuffix(output.File.Name, filepath.Ext(output.File.Name)) + "." + strings.ToLower(format)
				if err := tx.Model(&models.File{}).Where("id = ? AND owner_id = ?", output.FileID, j.OwnerID).
					Updates(map[string]any{"name": name, "status": "GENERATING"}).Error; err != nil {
					return err
				}
				if i == 0 {
					updates["output_path"] = key
				}
			}
			if err := tx.Model(&j).Updates(updates).Error; err != nil {
				return err
			}
			if err := outbox.EnqueueJob(tx, j.ID, time.Now()); err != nil {
				return err
			}
			selected++
		}
		if selected == 0 {
			return ErrNoJobsToRetry
		}
		return jobstate.Aggregate(tx, &parent.ID)
	})
	if err != nil {
		return nil, err
	}
	return s.GetTask(ctx, input.OwnerID, input.TaskID)
}

func (s *Service) CancelTask(ctx context.Context, ownerID uuid.UUID, taskID uuid.UUID) (*models.Task, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parent models.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&parent, "id = ? AND owner_id = ?", taskID, ownerID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTaskNotFound
			}
			return err
		}
		var jobs []models.Job
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Outputs").
			Where("task_id = ?", taskID).Order("id").Find(&jobs).Error; err != nil {
			return err
		}
		for _, j := range jobs {
			if !jobstate.Terminal(j.Status) {
				if err := tx.Model(&j).Updates(map[string]any{
					"status":           models.JobCancelled,
					"error_message":    "Task cancelled by user",
					"error_code":       "cancelled",
					"lease_expires_at": nil,
				}).Error; err != nil {
					return fmt.Errorf("cancel job %s: %w", j.ID, err)
				}
				if err := jobstate.OutputStatus(tx, &j, "FAILED"); err != nil {
					return err
				}
				if j.AttemptID != nil {
					if err := tx.Model(&models.JobAttempt{}).Where("id = ?", *j.AttemptID).
						Updates(map[string]any{"status": "CANCELLED", "finished_at": time.Now()}).Error; err != nil {
						return err
					}
					if err := outbox.CancelAttempt(tx, *j.AttemptID); err != nil {
						return err
					}
				}
			}
		}
		return jobstate.Aggregate(tx, &parent.ID)
	})

	if err != nil {
		return nil, err
	}

	return s.GetTask(ctx, ownerID, taskID)
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

	query := s.db.WithContext(ctx).Model(&models.Task{}).Where("owner_id = ?", ownerID)

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
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == "23505"
	}
	if pqErr, ok := errors.AsType[*pq.Error](err); ok {
		return pqErr.Code == "23505"
	}
	return errors.Is(err, gorm.ErrDuplicatedKey)
}
