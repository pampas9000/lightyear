package jobstate

import (
	"errors"

	"transcoder/server/internal/models"
	taskservice "transcoder/server/internal/services/taskstatus"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Lock always acquires the parent task before its jobs. Retry/cancel use the same order.
func Lock(tx *gorm.DB, id uuid.UUID) (*models.Job, error) {
	var ref models.Job
	if err := tx.Select("id", "task_id").First(&ref, "id = ?", id).Error; err != nil {
		return nil, err
	}
	if ref.TaskID != nil {
		var task models.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, "id = ?", *ref.TaskID).Error; err != nil {
			return nil, err
		}
	}
	var job models.Job
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Preload("Inputs", func(db *gorm.DB) *gorm.DB { return db.Order("position") }).
		Preload("Outputs", func(db *gorm.DB) *gorm.DB { return db.Order("position") }).
		First(&job, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func Aggregate(tx *gorm.DB, taskID *uuid.UUID) error {
	if taskID == nil {
		return nil
	}
	var statuses []models.JobStatus
	if err := tx.Model(&models.Job{}).Where("task_id = ?", *taskID).Pluck("status", &statuses).Error; err != nil {
		return err
	}
	return tx.Model(&models.Task{}).Where("id = ?", *taskID).Update("status", taskservice.Aggregate(statuses)).Error
}

func Terminal(status models.JobStatus) bool {
	return status == models.JobCompleted || status == models.JobFailed || status == models.JobCancelled
}

func OutputStatus(tx *gorm.DB, job *models.Job, status string) error {
	ids := make([]uuid.UUID, 0, len(job.Outputs)+1)
	for _, output := range job.Outputs {
		ids = append(ids, output.FileID)
	}
	if len(ids) == 0 && job.OutputFileID != nil {
		ids = append(ids, *job.OutputFileID)
	}
	if len(ids) == 0 {
		return nil
	}
	res := tx.Model(&models.File{}).Where("id IN ? AND owner_id = ?", ids, job.OwnerID).Update("status", status)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != int64(len(ids)) {
		return errors.New("output file missing or owner mismatch")
	}
	return nil
}
