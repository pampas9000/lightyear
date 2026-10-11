package taskstatus

import "transcoder/server/internal/models"

func Aggregate(statuses []models.JobStatus) models.TaskStatus {
	if len(statuses) == 0 {
		return models.TaskPending
	}
	pending, processing, completed, failed, cancelled := 0, 0, 0, 0, 0
	for _, status := range statuses {
		switch status {
		case models.JobPending:
			pending++
		case models.JobProcessing:
			processing++
		case models.JobCompleted:
			completed++
		case models.JobFailed:
			failed++
		case models.JobCancelled:
			cancelled++
		}
	}
	switch {
	case pending == len(statuses):
		return models.TaskPending
	case pending+processing > 0:
		return models.TaskProcessing
	case completed == len(statuses):
		return models.TaskCompleted
	case cancelled == len(statuses):
		return models.TaskCancelled
	case completed > 0:
		return models.TaskPartiallyFailed
	case failed > 0:
		return models.TaskFailed
	default:
		return models.TaskCancelled
	}
}
