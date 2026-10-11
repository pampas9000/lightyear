package storage

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// IsFileReferenced checks both legacy job columns and generalized input/output
// relations before a physical object is removed.
func IsFileReferenced(db *gorm.DB, fileID uuid.UUID) (bool, error) {
	queries := []struct {
		table string
		where string
		args  []any
	}{
		{
			table: "jobs",
			where: "input_file_id = ? OR output_file_id = ?",
			args:  []any{fileID, fileID},
		},
		{
			table: "job_inputs",
			where: "file_id = ?",
			args:  []any{fileID},
		},
		{
			table: "job_outputs",
			where: "file_id = ?",
			args:  []any{fileID},
		},
	}
	for _, query := range queries {
		var count int64
		if err := db.Table(query.table).Where(query.where, query.args...).Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}
