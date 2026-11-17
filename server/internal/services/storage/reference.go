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

// FreshEnough is the SQL condition (on "files") for a stored file that may
// still be handed out again, for reuse or fast upload: retention is not
// scheduled, or at least half of it is left. A reused file therefore lives on
// for a while, and objects never outlive their retention, which keeps a
// storage-side lifecycle rule safe. Its one argument is the current time.
const FreshEnough = `(files.expires_at IS NULL OR files.expires_at - ? > (files.expires_at - COALESCE(files.uploaded_at, files.created_at)) / 2)`
