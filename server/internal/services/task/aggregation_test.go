package task

import (
	"testing"
	"transcoder/server/internal/models"
)

func TestAggregateTaskStatus(t *testing.T) {
	tests := []struct {
		name     string
		statuses []models.JobStatus
		expected models.TaskStatus
	}{
		{
			name:     "empty statuses defaults to pending",
			statuses: []models.JobStatus{},
			expected: models.TaskPending,
		},
		{
			name:     "all pending is pending",
			statuses: []models.JobStatus{models.JobPending, models.JobPending},
			expected: models.TaskPending,
		},
		{
			name:     "mixed pending and processing is processing",
			statuses: []models.JobStatus{models.JobPending, models.JobProcessing},
			expected: models.TaskProcessing,
		},
		{
			name:     "mixed completed and pending is processing",
			statuses: []models.JobStatus{models.JobCompleted, models.JobPending},
			expected: models.TaskProcessing,
		},
		{
			name:     "all completed is completed",
			statuses: []models.JobStatus{models.JobCompleted, models.JobCompleted},
			expected: models.TaskCompleted,
		},
		{
			name:     "all failed is failed",
			statuses: []models.JobStatus{models.JobFailed, models.JobFailed},
			expected: models.TaskFailed,
		},
		{
			name:     "mixed completed and failed is partially failed",
			statuses: []models.JobStatus{models.JobCompleted, models.JobFailed},
			expected: models.TaskPartiallyFailed,
		},
		{
			name:     "mixed processing, completed, failed is processing",
			statuses: []models.JobStatus{models.JobProcessing, models.JobCompleted, models.JobFailed},
			expected: models.TaskProcessing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AggregateTaskStatus(tt.statuses)
			if got != tt.expected {
				t.Errorf("AggregateTaskStatus() = %v, expected %v", got, tt.expected)
			}
		})
	}
}
