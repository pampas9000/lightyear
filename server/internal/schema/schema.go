package schema

import (
	"transcoder/server/internal/models"

	"gorm.io/gorm"
)

// tables lists every table the application owns, for Reset.
var tables = []any{
	&models.APIKey{}, &models.Attempt{}, &models.Job{}, &models.Task{}, &models.Workflow{},
	&models.DraftFile{}, &models.File{}, &models.UserOauthAccount{}, &models.User{},
}

func Migrate(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := lockMigration(tx); err != nil {
			return err
		}
		if err := tx.Exec(`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
				WHERE t.typname = 'job_status' AND n.nspname = current_schema()
			) THEN
				CREATE TYPE job_status AS ENUM ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED', 'CANCELLED');
			END IF;
		END $$;`).Error; err != nil {
			return err
		}
		if err := tx.AutoMigrate(
			&models.User{}, &models.UserOauthAccount{}, &models.File{}, &models.DraftFile{},
			&models.Workflow{}, &models.Task{}, &models.Job{}, &models.Attempt{}, &models.APIKey{},
		); err != nil {
			return err
		}
		return tx.Exec(dispatchIndexes).Error
	})
}

// dispatchIndexes back the orchestrator's fair dispatch query: a skip scan
// over the owners of PENDING jobs, each owner's due jobs in (next_run_at, id)
// order, and each owner's PROCESSING count. idx_jobs_due is the global
// (status, next_run_at) index the previous dispatch query used.
const dispatchIndexes = `
DROP INDEX IF EXISTS idx_jobs_due;
CREATE INDEX IF NOT EXISTS idx_jobs_pending_owner ON jobs (owner_id, next_run_at, id) WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS idx_jobs_processing_owner ON jobs (owner_id) WHERE status = 'PROCESSING';`

// Reset drops every application table, including ones from earlier schema
// versions, and migrates from scratch. Development only: all data is lost.
func Reset(db *gorm.DB) error {
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := lockMigration(tx); err != nil {
			return err
		}
		legacy := []string{"job_inputs", "job_outputs", "job_attempts", "outbox_events", "provider_invocations"}
		for _, name := range legacy {
			if err := tx.Migrator().DropTable(name); err != nil {
				return err
			}
		}
		if err := tx.Migrator().DropTable(tables...); err != nil {
			return err
		}
		return tx.Exec(`DROP TYPE IF EXISTS job_status`).Error
	}); err != nil {
		return err
	}
	return Migrate(db)
}

func lockMigration(db *gorm.DB) error {
	return db.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('lightyear:schema:' || current_schema(), 0))`).Error
}
