package job

import (
	"context"

	"transcoder/server/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Service reads single jobs; jobs are created through tasks.
type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

// GetJob returns a single job belonging to the authenticated owner.
func (s *Service) GetJob(ctx context.Context, ownerID, id uuid.UUID) (*models.Job, error) {
	var j models.Job
	if err := s.db.WithContext(ctx).Preload("SourceFile").Preload("OutputFile").
		First(&j, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
		return nil, err
	}
	return &j, nil
}
