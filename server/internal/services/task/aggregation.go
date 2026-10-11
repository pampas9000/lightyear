package task

import (
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/taskstatus"
)

func AggregateTaskStatus(statuses []models.JobStatus) models.TaskStatus {
	return taskstatus.Aggregate(statuses)
}
