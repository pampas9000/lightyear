package schema

import (
	"transcoder/server/internal/models"

	"gorm.io/gorm"
)

func Migrate(db *gorm.DB) error {
	// Newly added enum values must commit before legacy rows can use them.
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := lockMigration(tx); err != nil {
			return err
		}
		if err := ensureJobStatusEnum(tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := lockMigration(tx); err != nil {
			return err
		}
		if err := upgradeJobStatusColumn(tx); err != nil {
			return err
		}
		if err := tx.AutoMigrate(
			&models.User{}, &models.UserOauthAccount{}, &models.File{}, &models.DraftFile{},
			&models.Workflow{}, &models.Task{}, &models.Job{},
			&models.JobInput{}, &models.JobOutput{}, &models.JobAttempt{},
			&models.OutboxEvent{}, &models.ProviderInvocation{},
		); err != nil {
			return err
		}
		// Legacy single-file jobs retain their HTTP fields while gaining relations.
		for _, sql := range []string{
			`UPDATE jobs SET operation_params = params::jsonb WHERE operation_params IS NULL AND params IS NOT NULL`,
			`INSERT INTO job_inputs (id, created_at, updated_at, job_id, file_id, role, position, path)
			 SELECT gen_random_uuid(), created_at, updated_at, id, input_file_id, 'source', 0, input_path
			 FROM jobs WHERE input_file_id IS NOT NULL
			 ON CONFLICT (job_id, position) DO NOTHING`,
			`INSERT INTO job_outputs (id, created_at, updated_at, job_id, file_id, role, position, path)
			 SELECT gen_random_uuid(), created_at, updated_at, id, output_file_id, 'result', 0, output_path
			 FROM jobs WHERE output_file_id IS NOT NULL
			 ON CONFLICT (job_id, position) DO NOTHING`,
		} {
			if err := tx.Exec(sql).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func lockMigration(db *gorm.DB) error {
	return db.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('lightyear:schema:' || current_schema(), 0))`).Error
}

func ensureJobStatusEnum(db *gorm.DB) error {
	return db.Exec(`DO $$
	BEGIN
		IF NOT EXISTS (
			SELECT 1
			FROM pg_type t
			JOIN pg_namespace n ON n.oid = t.typnamespace
			WHERE t.typname = 'job_status' AND n.nspname = current_schema()
		) THEN
			CREATE TYPE job_status AS ENUM ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED', 'CANCELLED');
		ELSE
			ALTER TYPE job_status ADD VALUE IF NOT EXISTS 'CANCELLED';
		END IF;
	END $$;`).Error
}

func upgradeJobStatusColumn(db *gorm.DB) error {
	return db.Exec(`DO $$
	BEGIN
		IF EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'jobs'
			  AND column_name = 'status'
			  AND data_type = 'character varying'
		) THEN
			ALTER TABLE jobs ALTER COLUMN status DROP DEFAULT;
			ALTER TABLE jobs
				ALTER COLUMN status TYPE job_status
				USING upper(status)::job_status;
			ALTER TABLE jobs ALTER COLUMN status SET DEFAULT 'PENDING'::job_status;
		END IF;
	END $$;`).Error
}
