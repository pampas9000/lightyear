package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"transcoder/server/internal/models"
	"transcoder/server/internal/operation"
	"transcoder/server/internal/services/jobstate"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Invocations struct {
	db *gorm.DB
}

func NewInvocations(db *gorm.DB) *Invocations {
	return &Invocations{db: db}
}

func (s *Invocations) Prepare(ctx context.Context, jobID, attemptID uuid.UUID, provider string) (*models.ProviderInvocation, error) {
	if strings.TrimSpace(provider) == "" {
		return nil, fmt.Errorf("provider is required")
	}
	var result models.ProviderInvocation
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		job, err := jobstate.Lock(tx, jobID)
		if err != nil {
			return err
		}
		if job.Status != models.JobProcessing || job.AttemptID == nil || *job.AttemptID != attemptID || job.Operation == operation.Transcode {
			return ErrInactiveAttempt
		}
		result = models.ProviderInvocation{JobID: job.ID, Generation: job.Generation,
			Provider: provider, Phase: "PREPARED",
			IdempotencyKey: fmt.Sprintf("%s:%s:%d", provider, job.ID, job.Generation)}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "job_id"}, {Name: "generation"}}, DoNothing: true}).
			Create(&result).Error; err != nil {
			return err
		}
		result = models.ProviderInvocation{}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&result, "job_id = ? AND generation = ?", job.ID, job.Generation).Error; err != nil {
			return err
		}
		if result.Provider != provider {
			return fmt.Errorf("provider cannot change within one execution generation")
		}
		return nil
	})
	return &result, err
}

// BeginSubmit is the only transition granting permission for a paid API call.
// A crash after this commit is ambiguous, even if no HTTP response was received.
func (s *Invocations) BeginSubmit(ctx context.Context, invocationID, attemptID uuid.UUID) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ref models.ProviderInvocation
		if err := tx.First(&ref, "id = ?", invocationID).Error; err != nil {
			return err
		}
		job, err := jobstate.Lock(tx, ref.JobID)
		if err != nil {
			return err
		}
		if job.Status != models.JobProcessing || job.AttemptID == nil || *job.AttemptID != attemptID || job.Generation != ref.Generation {
			return ErrInactiveAttempt
		}
		var invocation models.ProviderInvocation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&invocation, "id = ?", invocationID).Error; err != nil {
			return err
		}
		switch invocation.Phase {
		case "PREPARED":
			return tx.Model(&invocation).Update("phase", "SUBMITTING").Error
		case "SUBMITTING", "UNKNOWN":
			return ErrReconciliationRequired
		default:
			return ErrAlreadySubmitted
		}
	})
}

func (s *Invocations) MarkUnknown(ctx context.Context, id uuid.UUID, message string) error {
	return s.db.WithContext(ctx).Model(&models.ProviderInvocation{}).
		Where("id = ? AND phase = ?", id, "SUBMITTING").
		Updates(map[string]any{"phase": "UNKNOWN", "last_error": message}).Error
}

// RecordReceipt also accepts a late receipt after cancellation: facts about a
// remote request must survive even when its output will no longer be published.
func (s *Invocations) RecordReceipt(ctx context.Context, id uuid.UUID, receipt Receipt) error {
	if receipt.RequestID == "" && receipt.RemoteTaskID == "" {
		return fmt.Errorf("provider receipt requires a request or remote task ID")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var invocation models.ProviderInvocation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&invocation, "id = ?", id).Error; err != nil {
			return err
		}
		if (invocation.RemoteTaskID != "" && invocation.RemoteTaskID != receipt.RemoteTaskID) ||
			(invocation.RequestID != "" && invocation.RequestID != receipt.RequestID) {
			return fmt.Errorf("provider receipt mismatch")
		}
		if invocation.Phase == "COMPLETED" || invocation.Phase == "FAILED" {
			return nil
		}
		if invocation.Phase != "SUBMITTING" && invocation.Phase != "UNKNOWN" && invocation.Phase != "SUBMITTED" {
			return fmt.Errorf("invocation has not entered submission")
		}
		return tx.Model(&invocation).Updates(map[string]any{
			"phase": "SUBMITTED", "remote_task_id": receipt.RemoteTaskID,
			"request_id": receipt.RequestID, "next_poll_at": receipt.NextPollAt, "last_error": "",
		}).Error
	})
}

func (s *Invocations) RecordOutcome(ctx context.Context, id uuid.UUID, failed bool, usage *Usage) error {
	var raw operation.JSON
	if usage != nil {
		if usage.CostMinor < 0 || len(usage.Currency) != 3 {
			return fmt.Errorf("usage requires a nonnegative minor-unit cost and a currency code")
		}
		encoded, err := json.Marshal(usage)
		if err != nil {
			return err
		}
		raw = operation.JSON(encoded)
	}
	phase := "COMPLETED"
	if failed {
		phase = "FAILED"
	}
	res := s.db.WithContext(ctx).Model(&models.ProviderInvocation{}).
		Where("id = ? AND phase = ?", id, "SUBMITTED").
		Updates(map[string]any{"phase": phase, "usage": raw, "next_poll_at": nil})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrAlreadySubmitted
	}
	return nil
}

// ConfirmNotAccepted is only for an authoritative rejection or reconciliation.
// Timeouts and "not found yet" are insufficient evidence to call this method.
func (s *Invocations) ConfirmNotAccepted(ctx context.Context, id uuid.UUID) error {
	res := s.db.WithContext(ctx).Model(&models.ProviderInvocation{}).
		Where("id = ? AND phase IN ? AND request_id = '' AND remote_task_id = ''", id, []string{"SUBMITTING", "UNKNOWN"}).
		Updates(map[string]any{"phase": "PREPARED", "last_error": ""})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrAlreadySubmitted
	}
	return nil
}
