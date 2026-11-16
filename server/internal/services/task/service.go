package task

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"transcoder/server/internal/compression"
	"transcoder/server/internal/imageoptions"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/jobstate"
	taskqueue "transcoder/server/internal/task"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

var (
	ErrNoItemsProvided     = errors.New("at least one item is required to create a task")
	ErrTooManyItems        = errors.New("at most 100 items are allowed per task")
	ErrOptionsRequired     = errors.New("exactly one of options, outputs, or workflow_id is required")
	ErrTargetNotAllowed    = errors.New("target is only allowed alongside top-level options")
	ErrRetrySingleJob      = errors.New("changing options or target requires specifying a single job")
	ErrWorkflowNotFound    = errors.New("workflow not found")
	ErrInvalidFileID       = errors.New("file_id is required and must not be nil")
	ErrFileNotFound        = errors.New("file not found or not uploaded")
	ErrIdempotencyConflict = errors.New("idempotency key conflict: request payload mismatch")
	ErrIdempotencyKey      = errors.New("idempotency key is too long (max 255 bytes)")
	ErrTaskNotFound        = errors.New("task not found")
	ErrNoJobsToRetry       = errors.New("no failed jobs available to retry")
	ErrJobNotRetryable     = errors.New("active jobs cannot be retried")
	ErrInvalidJobID        = errors.New("job does not belong to task")
)

const maxItems = compression.MaxItems

// MaxIdempotencyKeyLen is the longest accepted idempotency key, in bytes.
const MaxIdempotencyKeyLen = 255

type CreateTaskInput struct {
	OwnerID    uuid.UUID
	WorkflowID *uuid.UUID
	Items      []TaskItemInput
	// Options (+ optional Target), Outputs, and WorkflowID are mutually exclusive.
	Options        json.RawMessage
	Target         json.RawMessage
	Outputs        json.RawMessage
	IdempotencyKey string
}

type TaskItemInput struct {
	FileID uuid.UUID `json:"file_id"`
}

type Service struct {
	db    *gorm.DB
	kv    *redis.Client
	queue taskqueue.Queue
}

func NewService(db *gorm.DB, kv *redis.Client) *Service {
	s := &Service{db: db, kv: kv}
	if kv != nil {
		s.queue = taskqueue.RedisQueue{Client: kv}
	}
	return s
}

// fingerprint identifies a creation request for idempotency: the same files
// with the same effective outputs (item order is irrelevant).
func fingerprint(workflowID *uuid.UUID, outputs []compression.OutputSpec, items []TaskItemInput) (string, error) {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.FileID.String()
	}
	slices.Sort(ids)
	raw, err := json.Marshal(struct {
		WorkflowID *uuid.UUID               `json:"workflow_id,omitempty"`
		Outputs    []compression.OutputSpec `json:"outputs"`
		Files      []string                 `json:"files"`
	}{workflowID, outputs, ids})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) resolveOutputs(ctx context.Context, input CreateTaskInput) ([]compression.OutputSpec, error) {
	hasOptions := compression.HasJSONValue(input.Options)
	hasOutputs := compression.HasJSONValue(input.Outputs)
	hasWorkflow := input.WorkflowID != nil
	sources := 0
	for _, present := range []bool{hasOptions, hasOutputs, hasWorkflow} {
		if present {
			sources++
		}
	}
	if sources != 1 {
		return nil, ErrOptionsRequired
	}
	if !hasOptions && compression.HasJSONValue(input.Target) {
		return nil, ErrTargetNotAllowed
	}
	if hasOptions {
		spec, err := compression.ParseSingleOutput(input.Options, input.Target)
		if err != nil {
			return nil, err
		}
		return []compression.OutputSpec{spec}, nil
	}
	if hasOutputs {
		return compression.ParseOutputsJSON(input.Outputs)
	}
	var wf models.Workflow
	if err := s.db.WithContext(ctx).First(&wf, "id = ? AND owner_id = ?", *input.WorkflowID, input.OwnerID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkflowNotFound
		}
		return nil, fmt.Errorf("fetch workflow: %w", err)
	}
	canonical, err := compression.ValidateWorkflowOutputs(wf.Outputs)
	if err != nil {
		return nil, err
	}
	return []compression.OutputSpec(canonical), nil
}

func (s *Service) CreateTask(ctx context.Context, input CreateTaskInput) (*models.Task, error) {
	t, _, err := s.Create(ctx, input)
	return t, err
}

// Create is CreateTask that also reports whether the result is the task an
// earlier request with the same idempotency key created (a replay).
func (s *Service) Create(ctx context.Context, input CreateTaskInput) (*models.Task, bool, error) {
	if len(input.Items) == 0 {
		return nil, false, ErrNoItemsProvided
	}
	if len(input.Items) > maxItems {
		return nil, false, ErrTooManyItems
	}
	for _, item := range input.Items {
		if item.FileID == uuid.Nil {
			return nil, false, ErrInvalidFileID
		}
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if len(key) > MaxIdempotencyKeyLen {
		return nil, false, ErrIdempotencyKey
	}
	outputs, err := s.resolveOutputs(ctx, input)
	if err != nil {
		return nil, false, err
	}
	if len(input.Items)*len(outputs) > compression.MaxTotalJobs {
		return nil, false, fmt.Errorf("%w: %d items x %d outputs exceeds limit %d",
			compression.ErrTooManyJobs, len(input.Items), len(outputs), compression.MaxTotalJobs)
	}
	fp, err := fingerprint(input.WorkflowID, outputs, input.Items)
	if err != nil {
		return nil, false, err
	}
	existing := func() (*models.Task, bool, error) {
		var t models.Task
		err := s.db.WithContext(ctx).Where("idempotency_key = ? AND owner_id = ?", key, input.OwnerID).First(&t).Error
		if err != nil {
			return nil, false, err
		}
		if t.RequestFingerprint != fp {
			return nil, false, ErrIdempotencyConflict
		}
		got, err := s.GetTask(ctx, input.OwnerID, t.ID)
		return got, err == nil, err
	}
	if key != "" {
		if t, replayed, err := existing(); !errors.Is(err, gorm.ErrRecordNotFound) {
			return t, replayed, err
		}
	}

	var taskID uuid.UUID
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		files, err := lockSourceFiles(tx, input.OwnerID, input.Items)
		if err != nil {
			return err
		}
		t := models.Task{OwnerID: input.OwnerID, Status: models.TaskPending,
			WorkflowID: input.WorkflowID, RequestFingerprint: fp}
		if key != "" {
			t.IdempotencyKey = &key
		}
		if err := tx.Create(&t).Error; err != nil {
			return err
		}
		taskID = t.ID
		now := time.Now()
		for _, item := range input.Items {
			fileID := files[item.FileID].ID
			for outIdx, out := range outputs {
				job := models.Job{
					OwnerID:      input.OwnerID,
					TaskID:       t.ID,
					WorkflowID:   input.WorkflowID,
					OutputIndex:  outIdx,
					OutputName:   out.Name,
					Operation:    models.OpImageConvert,
					Options:      out.Options,
					Target:       out.Target,
					SourceFileID: fileID,
					Status:       models.JobPending,
					NextRunAt:    &now,
					MaxAttempts:  3,
				}
				if err := reuseOutput(tx, &job); err != nil {
					return err
				}
				if err := tx.Create(&job).Error; err != nil {
					return fmt.Errorf("create job: %w", err)
				}
			}
		}
		return jobstate.Aggregate(tx, t.ID)
	})
	if err != nil {
		if key != "" && isUniqueConstraintViolation(err) {
			return existing()
		}
		return nil, false, err
	}
	t, err := s.GetTask(ctx, input.OwnerID, taskID)
	return t, false, err
}

// lockSourceFiles locks drafts before files, in UUID order (cleanup uses the
// same order), then consumes the drafts.
func lockSourceFiles(tx *gorm.DB, owner uuid.UUID, items []TaskItemInput) (map[uuid.UUID]models.File, error) {
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		if !slices.Contains(ids, item.FileID) {
			ids = append(ids, item.FileID)
		}
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return cmp.Compare(a.String(), b.String()) })
	var drafts []models.DraftFile
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("file_id IN ? AND owner_id = ?", ids, owner).Order("file_id").Find(&drafts).Error; err != nil {
		return nil, err
	}
	files := make(map[uuid.UUID]models.File, len(ids))
	for _, id := range ids {
		var f models.File
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&f, "id = ? AND owner_id = ? AND status = ?", id, owner, "UPLOADED").Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrFileNotFound
			}
			return nil, fmt.Errorf("lock input file %q: %w", id, err)
		}
		files[id] = f
	}
	if err := tx.Where("file_id IN ? AND owner_id = ?", ids, owner).Delete(&models.DraftFile{}).Error; err != nil {
		return nil, fmt.Errorf("delete draft files: %w", err)
	}
	return files, nil
}

// reuseOutput sets the job's artifact key and, when the same owner already has
// that artifact, completes the job with it instead of queueing work.
func reuseOutput(tx *gorm.DB, job *models.Job) error {
	key, err := compression.ArtifactKey(job.SourceFileID.String(), job.Options, job.Target)
	if err != nil {
		return err
	}
	job.ArtifactKey = key
	var done models.Job
	err = tx.Preload("OutputFile").
		Joins("JOIN files ON files.id = jobs.output_file_id AND files.status = ?", "UPLOADED").
		Where("jobs.owner_id = ? AND jobs.artifact_key = ? AND jobs.status = ?", job.OwnerID, key, models.JobCompleted).
		Order("jobs.created_at DESC").First(&done).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	job.Status, job.Progress, job.OutputFileID, job.NextRunAt = models.JobCompleted, 100, done.OutputFileID, nil
	resolved := done.Options
	if done.ResolvedOptions != nil {
		resolved = *done.ResolvedOptions
	}
	job.ResolvedOptions = &resolved
	if done.OutputFile != nil {
		job.TargetMet = compression.IsTargetMet(done.OutputFile.Size, job.Target)
		if job.Target != nil {
			job.Achieved = &compression.Achieved{Bytes: new(done.OutputFile.Size)}
		} else {
			job.Achieved = nil
		}
	} else {
		job.TargetMet = nil
		job.Achieved = nil
	}
	return nil
}

func (s *Service) GetTask(ctx context.Context, ownerID uuid.UUID, id uuid.UUID) (*models.Task, error) {
	var t models.Task
	if err := s.db.WithContext(ctx).
		Preload("Jobs", func(db *gorm.DB) *gorm.DB { return db.Order("id, output_index") }).
		Preload("Jobs.SourceFile").Preload("Jobs.OutputFile").
		First(&t, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
		return nil, err
	}
	models.PopulateJobsDerived(t.Jobs)
	return &t, nil
}

type RetryTaskInput struct {
	OwnerID  uuid.UUID
	TaskID   uuid.UUID
	JobIDs   []uuid.UUID
	RetryAll bool
	// Options and Target, when present, replace the retried job's settings (single job only).
	Options json.RawMessage
	Target  json.RawMessage
}

func (s *Service) RetryTask(ctx context.Context, input RetryTaskInput) (*models.Task, error) {
	hasOptions := compression.HasJSONValue(input.Options)
	hasTargetField := len(bytes.TrimSpace(input.Target)) > 0
	changingParams := hasOptions || hasTargetField
	if changingParams && (len(input.JobIDs) != 1 || input.RetryAll) {
		return nil, ErrRetrySingleJob
	}
	var parsedTarget *compression.Target
	if hasTargetField {
		var err error
		parsedTarget, err = compression.ParseTarget(input.Target)
		if err != nil {
			return nil, err
		}
	}
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
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("task_id = ?", parent.ID).Order("id, output_index").Find(&jobs).Error; err != nil {
			return err
		}
		for _, id := range input.JobIDs {
			if !slices.ContainsFunc(jobs, func(j models.Job) bool { return j.ID == id }) {
				return ErrInvalidJobID
			}
		}
		now, selected := time.Now(), 0
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
			if changingParams {
				newTarget := j.Target
				if hasTargetField {
					newTarget = parsedTarget
				}
				var (
					newOpts imageoptions.Options
					err     error
				)
				switch {
				case hasOptions && newTarget != nil:
					newOpts, err = imageoptions.ParseForTarget(input.Options)
				case hasOptions:
					newOpts, err = imageoptions.Parse(input.Options)
				case newTarget != nil:
					opts := j.Options
					if j.Target == nil && !opts.Lossless {
						// Adding a target to a non-target job without new options must clear
						// the old default/fixed quality so the worker searches for the target.
						opts.Quality = nil
						opts.Distance = nil
					}
					newOpts, err = opts.CanonicalizeForTarget()
				default:
					newOpts, err = j.Options.Canonicalize()
				}
				if err != nil {
					return err
				}
				if err := compression.ValidateTarget(newOpts, newTarget); err != nil {
					return err
				}
				j.Options, j.Target = newOpts, newTarget
			}
			j.Status, j.Progress, j.Attempts, j.NextRunAt = models.JobPending, 0, 0, &now
			j.CurrentAttemptID, j.LeaseExpiresAt, j.OutputFileID = nil, nil, nil
			j.ResolvedOptions, j.TargetMet, j.Achieved = nil, nil, nil
			j.ErrorCode, j.ErrorMessage = "", ""
			if err := reuseOutput(tx, &j); err != nil {
				return err
			}
			updates := map[string]any{
				"options":            j.Options,
				"target":             nil,
				"resolved_options":   nil,
				"target_met":         j.TargetMet,
				"achieved":           nil,
				"status":             j.Status,
				"progress":           j.Progress,
				"attempts":           j.Attempts,
				"next_run_at":        j.NextRunAt,
				"current_attempt_id": j.CurrentAttemptID,
				"lease_expires_at":   j.LeaseExpiresAt,
				"output_file_id":     j.OutputFileID,
				"error_code":         j.ErrorCode,
				"error_message":      j.ErrorMessage,
				"artifact_key":       j.ArtifactKey,
			}
			if j.Target != nil {
				updates["target"] = *j.Target
			}
			if j.ResolvedOptions != nil {
				updates["resolved_options"] = *j.ResolvedOptions
			}
			if j.Achieved != nil {
				updates["achieved"] = *j.Achieved
			}
			if err := tx.Model(&j).Updates(updates).Error; err != nil {
				return err
			}
			selected++
		}
		if selected == 0 {
			return ErrNoJobsToRetry
		}
		return jobstate.Aggregate(tx, parent.ID)
	})
	if err != nil {
		return nil, err
	}
	return s.GetTask(ctx, input.OwnerID, input.TaskID)
}

func (s *Service) CancelTask(ctx context.Context, ownerID uuid.UUID, taskID uuid.UUID) (*models.Task, error) {
	var running []uuid.UUID
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
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("task_id = ?", taskID).Order("id").Find(&jobs).Error; err != nil {
			return err
		}
		now := time.Now()
		for _, j := range jobs {
			if jobstate.Terminal(j.Status) {
				continue
			}
			if err := tx.Model(&j).Updates(map[string]any{
				"status": models.JobCancelled, "error_code": "cancelled",
				"error_message": "Task cancelled by user", "lease_expires_at": nil, "next_run_at": nil,
			}).Error; err != nil {
				return fmt.Errorf("cancel job %s: %w", j.ID, err)
			}
			if j.CurrentAttemptID != nil {
				if err := tx.Model(&models.Attempt{}).Where("id = ?", *j.CurrentAttemptID).
					Updates(map[string]any{"status": models.AttemptCancelled, "finished_at": now}).Error; err != nil {
					return err
				}
				running = append(running, *j.CurrentAttemptID)
			}
		}
		return jobstate.Aggregate(tx, parent.ID)
	})
	if err != nil {
		return nil, err
	}
	// Best effort: a worker that misses the signal finishes, and its result is
	// ignored because the job is no longer PROCESSING.
	if s.queue != nil {
		for _, id := range running {
			if err := s.queue.Cancel(ctx, id.String()); err != nil {
				slog.Warn("signal attempt cancellation", "attempt_id", id, "error", err)
			}
		}
	}
	return s.GetTask(ctx, ownerID, taskID)
}

type ListTasksParams struct {
	Count  int
	Page   int
	Status string
}

// ListTasks returns one page of the owner's tasks and the total count.
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
	limit, offset := 20, 0
	if params.Count > 0 && params.Count <= 100 {
		limit = params.Count
	}
	if params.Page > 0 {
		offset = (params.Page - 1) * limit
	}
	if err := query.
		Preload("Jobs", func(db *gorm.DB) *gorm.DB { return db.Order("id, output_index") }).
		Preload("Jobs.SourceFile").
		Preload("Jobs.OutputFile").
		Order("created_at DESC").Limit(limit).Offset(offset).Find(&tasks).Error; err != nil {
		return nil, 0, err
	}
	for i := range tasks {
		models.PopulateJobsDerived(tasks[i].Jobs)
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

// GetStats summarizes the owner's tasks, storage and the active workers.
func (s *Service) GetStats(ctx context.Context, ownerID uuid.UUID) (*StatsResponse, error) {
	var results []struct {
		Status string `gorm:"column:status"`
		Count  int    `gorm:"column:count"`
	}
	if err := s.db.WithContext(ctx).Model(&models.Task{}).
		Select("status, count(id) as count").Where("owner_id = ?", ownerID).
		Group("status").Scan(&results).Error; err != nil {
		return nil, err
	}
	stats := &StatsResponse{StorageLimit: 10995116277760} // 10 TB
	for _, r := range results {
		switch models.TaskStatus(r.Status) {
		case models.TaskPending:
			stats.Pending = r.Count
		case models.TaskProcessing:
			stats.Processing = r.Count
		case models.TaskCompleted:
			stats.Completed = r.Count
		case models.TaskFailed:
			stats.Failed = r.Count
		case models.TaskPartiallyFailed:
			stats.PartiallyFailed = r.Count
		}
	}

	if err := s.db.WithContext(ctx).Model(&models.File{}).
		Select("COALESCE(SUM(size), 0)").Where("owner_id = ? AND status = ?", ownerID, "UPLOADED").
		Row().Scan(&stats.StorageUsed); err != nil {
		slog.Error("failed to query storage used", "owner_id", ownerID, "error", err)
	}

	// Workers create their consumer group on first start; until then there are none.
	if s.kv != nil {
		if consumers, err := s.kv.XInfoConsumers(ctx, taskqueue.QueueImage, taskqueue.WorkerGroup).Result(); err == nil {
			for _, c := range consumers {
				if c.Idle < 5*time.Minute {
					stats.ActiveWorkers++
				}
			}
		}
	}

	var dailyCounts []struct {
		Date  time.Time `gorm:"column:date"`
		Count int       `gorm:"column:count"`
	}
	if err := s.db.WithContext(ctx).Model(&models.Task{}).
		Select("date_trunc('day', created_at) as date, count(id) as count").
		Where("owner_id = ? AND created_at >= now() - interval '7 days'", ownerID).
		Group("date_trunc('day', created_at)").Order("date_trunc('day', created_at) ASC").
		Scan(&dailyCounts).Error; err != nil {
		slog.Error("failed to query daily task volume", "owner_id", ownerID, "error", err)
	}
	daily := make(map[string]int, len(dailyCounts))
	for _, dc := range dailyCounts {
		daily[dc.Date.Format("2006-01-02")] = dc.Count
	}
	stats.DailyChart = make([]int, 7)
	now := time.Now()
	for i := range 7 {
		stats.DailyChart[i] = daily[now.AddDate(0, 0, -6+i).Format("2006-01-02")]
	}

	var thisWeek, prevWeek int64
	s.db.WithContext(ctx).Model(&models.Task{}).
		Where("owner_id = ? AND created_at >= now() - interval '7 days'", ownerID).Count(&thisWeek)
	s.db.WithContext(ctx).Model(&models.Task{}).
		Where("owner_id = ? AND created_at >= now() - interval '14 days' AND created_at < now() - interval '7 days'", ownerID).
		Count(&prevWeek)
	switch {
	case prevWeek > 0:
		stats.GrowthRate = float64(thisWeek-prevWeek) / float64(prevWeek) * 100.0
	case thisWeek > 0:
		stats.GrowthRate = 100.0
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
