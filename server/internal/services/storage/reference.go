package storage

import (
	"transcoder/server/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// IsFileReferenced reports whether any job uses the file as its source or
// output, in which case its object must not be removed.
func IsFileReferenced(db *gorm.DB, fileID uuid.UUID) (bool, error) {
	var count int64
	err := db.Model(&models.Job{}).
		Where("source_file_id = ? OR output_file_id = ?", fileID, fileID).
		Count(&count).Error
	return count > 0, err
}
