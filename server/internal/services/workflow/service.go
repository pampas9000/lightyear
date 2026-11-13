package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"transcoder/server/internal/compression"
	"transcoder/server/internal/models"

	"gorm.io/gorm"

	"github.com/google/uuid"
)

var (
	ErrNameRequired     = errors.New("name is required")
	ErrOutputsRequired  = errors.New("exactly one of outputs or options is required")
	ErrWorkflowNotFound = errors.New("workflow not found")
)

type CreateWorkflowInput struct {
	OwnerID uuid.UUID
	Name    string
	// Outputs is preferred; Options (+ optional Target) is a single-output shorthand.
	Outputs json.RawMessage
	Options json.RawMessage
	Target  json.RawMessage
}

func (input CreateWorkflowInput) resolve() (string, compression.WorkflowOutputs, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return "", nil, ErrNameRequired
	}
	hasOutputs := compression.HasJSONValue(input.Outputs)
	hasOptions := compression.HasJSONValue(input.Options)
	hasTarget := compression.HasJSONValue(input.Target)
	if hasOutputs == hasOptions {
		return "", nil, ErrOutputsRequired
	}
	if hasOutputs && hasTarget {
		return "", nil, fmt.Errorf("%w: target is only allowed alongside top-level options", ErrOutputsRequired)
	}
	if hasOutputs {
		specs, err := compression.ParseOutputsJSON(input.Outputs)
		if err != nil {
			return "", nil, err
		}
		return name, compression.WorkflowOutputs(specs), nil
	}
	spec, err := compression.ParseSingleOutput(input.Options, input.Target)
	if err != nil {
		return "", nil, err
	}
	return name, compression.WorkflowOutputs{spec}, nil
}

type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) CreateWorkflow(ctx context.Context, input CreateWorkflowInput) (*models.Workflow, error) {
	name, outputs, err := input.resolve()
	if err != nil {
		return nil, err
	}
	wf := &models.Workflow{OwnerID: input.OwnerID, Name: name, Outputs: outputs}

	if err := s.db.WithContext(ctx).Create(wf).Error; err != nil {
		return nil, fmt.Errorf("create workflow record: %w", err)
	}

	return wf, nil
}

func (s *Service) GetWorkflow(ctx context.Context, ownerID, id uuid.UUID) (*models.Workflow, error) {
	var wf models.Workflow
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, ownerID).First(&wf).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkflowNotFound
		}
		return nil, err
	}
	return &wf, nil
}

func (s *Service) UpdateWorkflow(ctx context.Context, ownerID, id uuid.UUID, input CreateWorkflowInput) (*models.Workflow, error) {
	name, outputs, err := input.resolve()
	if err != nil {
		return nil, err
	}
	var wf models.Workflow
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, ownerID).First(&wf).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkflowNotFound
		}
		return nil, fmt.Errorf("fetch workflow: %w", err)
	}

	wf.Name, wf.Outputs = name, outputs

	if err := s.db.WithContext(ctx).Save(&wf).Error; err != nil {
		return nil, fmt.Errorf("update workflow record: %w", err)
	}

	return &wf, nil
}

// DeleteWorkflow removes the workflow and clears dangling references from
// tasks and jobs, which keep their own resolved options.
func (s *Service) DeleteWorkflow(ctx context.Context, ownerID, id uuid.UUID) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var wf models.Workflow
		if err := tx.Where("id = ? AND owner_id = ?", id, ownerID).First(&wf).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWorkflowNotFound
			}
			return fmt.Errorf("fetch workflow: %w", err)
		}

		if err := tx.Model(&models.Task{}).Where("workflow_id = ?", id).Update("workflow_id", nil).Error; err != nil {
			return fmt.Errorf("detach tasks: %w", err)
		}
		if err := tx.Model(&models.Job{}).Where("workflow_id = ?", id).Update("workflow_id", nil).Error; err != nil {
			return fmt.Errorf("detach jobs: %w", err)
		}

		if err := tx.Delete(&wf).Error; err != nil {
			return fmt.Errorf("delete workflow record: %w", err)
		}
		return nil
	})
}

func (s *Service) ListWorkflows(ctx context.Context, ownerID uuid.UUID) ([]models.Workflow, error) {
	var wfs []models.Workflow
	if err := s.db.WithContext(ctx).Where("owner_id = ?", ownerID).Find(&wfs).Error; err != nil {
		return nil, err
	}
	return wfs, nil
}
