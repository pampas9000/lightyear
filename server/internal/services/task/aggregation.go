package task

import (
	"transcoder/server/internal/models"
)

// AggregateTaskStatus aggregates individual job statuses to determine the task status.
func AggregateTaskStatus(jobStatuses []models.JobStatus) models.TaskStatus {
	if len(jobStatuses) == 0 {
		return models.TaskPending
	}

	var hasPending, hasProcessing, hasCompleted, hasFailed bool

	for _, s := range jobStatuses {
		switch s {
		case models.JobPending:
			hasPending = true
		case models.JobProcessing:
			hasProcessing = true
		case models.JobCompleted:
			hasCompleted = true
		case models.JobFailed:
			hasFailed = true
		}
	}

	total := len(jobStatuses)

	// If there are any pending or processing jobs, the task is either pending or processing
	if hasPending || hasProcessing {
		// If all jobs are pending, the task is pending
		pendingCount := 0
		for _, s := range jobStatuses {
			if s == models.JobPending {
				pendingCount++
			}
		}
		if pendingCount == total {
			return models.TaskPending
		}
		return models.TaskProcessing
	}

	// All jobs are terminal (Completed / Failed)
	if hasFailed {
		// If all are failed, the task is failed
		failedCount := 0
		for _, s := range jobStatuses {
			if s == models.JobFailed {
				failedCount++
			}
		}
		if failedCount == total {
			return models.TaskFailed
		}
		return models.TaskPartiallyFailed
	}

	if hasCompleted {
		return models.TaskCompleted
	}

	return models.TaskPending
}
