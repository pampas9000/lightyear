package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"transcoder/server/internal/imageoptions"
	"transcoder/server/internal/models"

	"gorm.io/gorm"

	"github.com/google/uuid"
)

var (
	ErrNameRequired     = errors.New("name is required")
	ErrWorkflowNotFound = errors.New("workflow not found")
)

type CreateWorkflowInput struct {
	OwnerID uuid.UUID
	Name    string
	// Options is the raw request body field, decoded strictly.
	Options json.RawMessage
}

func (input CreateWorkflowInput) resolve() (string, imageoptions.Options, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return "", imageoptions.Options{}, ErrNameRequired
	}
	options, err := imageoptions.Parse(input.Options)
	return name, options, err
}

type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) CreateWorkflow(ctx context.Context, input CreateWorkflowInput) (*models.Workflow, error) {
	name, options, err := input.resolve()
	if err != nil {
		return nil, err
	}
	wf := &models.Workflow{OwnerID: input.OwnerID, Name: name, Options: options}

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
	name, options, err := input.resolve()
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

	wf.Name, wf.Options = name, options

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
