package jobstate

import (
	"transcoder/server/internal/models"
	taskservice "transcoder/server/internal/services/taskstatus"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Lock always acquires the parent task before the job; every writer (dispatch,
// results, recovery, retry, cancel) uses this order.
func Lock(tx *gorm.DB, id uuid.UUID) (*models.Job, error) {
	var ref models.Job
	if err := tx.Select("id", "task_id").First(&ref, "id = ?", id).Error; err != nil {
		return nil, err
	}
	var task models.Task
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&task, "id = ?", ref.TaskID).Error; err != nil {
		return nil, err
	}
	var job models.Job
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func Aggregate(tx *gorm.DB, taskID uuid.UUID) error {
	var statuses []models.JobStatus
	if err := tx.Model(&models.Job{}).Where("task_id = ?", taskID).Pluck("status", &statuses).Error; err != nil {
		return err
	}
	return tx.Model(&models.Task{}).Where("id = ?", taskID).Update("status", taskservice.Aggregate(statuses)).Error
}

func Terminal(status models.JobStatus) bool {
	return status == models.JobCompleted || status == models.JobFailed || status == models.JobCancelled
}
