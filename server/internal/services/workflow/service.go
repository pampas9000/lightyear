package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"transcoder/server/internal/models"
	"transcoder/server/internal/transcode"

	"gorm.io/gorm"

	"github.com/google/uuid"
)

var (
	ErrNameRequired         = errors.New("name is required")
	ErrTargetFormatRequired = errors.New("targetFormat is required")
)

type CreateWorkflowInput struct {
	OwnerID      uuid.UUID
	Name         string
	TargetFormat string
	Params       transcode.Params
}

type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) CreateWorkflow(ctx context.Context, input CreateWorkflowInput) (*models.Workflow, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.TargetFormat = strings.TrimSpace(input.TargetFormat)

	if input.Name == "" {
		return nil, ErrNameRequired
	}
	if input.TargetFormat == "" {
		return nil, ErrTargetFormatRequired
	}

	if err := input.Params.Validate(input.TargetFormat); err != nil {
		return nil, fmt.Errorf("validate transcode params: %w", err)
	}

	wf := &models.Workflow{
		OwnerID:      input.OwnerID,
		Name:         input.Name,
		TargetFormat: input.TargetFormat,
		Params:       input.Params,
	}

	if err := s.db.WithContext(ctx).Create(wf).Error; err != nil {
		return nil, fmt.Errorf("create workflow record: %w", err)
	}

	return wf, nil
}

func (s *Service) GetWorkflow(ctx context.Context, id uuid.UUID) (*models.Workflow, error) {
	var wf models.Workflow
	if err := s.db.WithContext(ctx).First(&wf, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &wf, nil
}

func (s *Service) ListWorkflows(ctx context.Context, ownerID uuid.UUID) ([]models.Workflow, error) {
	var wfs []models.Workflow
	if err := s.db.WithContext(ctx).Where("owner_id = ?", ownerID).Find(&wfs).Error; err != nil {
		return nil, err
	}
	return wfs, nil
}
