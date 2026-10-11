package schema_test

import (
	"sync"
	"testing"

	"transcoder/server/internal/models"
	"transcoder/server/internal/schema"
	"transcoder/server/internal/testutil"
	"transcoder/server/internal/transcode"

	"github.com/google/uuid"
)

func TestMigrateIsIdempotentAndBackfillsLegacyRelations(t *testing.T) {
	db := testutil.OpenDatabase(t)
	ownerID := uuid.New()
	inputID := uuid.New()
	outputID := uuid.New()
	params := transcode.Params{Engine: "libavif:avif"}
	job := models.Job{
		OwnerID:      ownerID,
		Status:       models.JobPending,
		InputFileID:  &inputID,
		OutputFileID: &outputID,
		InputPath:    "uploads/source.png",
		OutputPath:   "outputs/result.avif",
		TargetFormat: "avif",
		Params:       params,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create legacy job: %v", err)
	}

	if err := schema.Migrate(db); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("second migration: %v", err)
	}

	var inputs []models.JobInput
	if err := db.Where("job_id = ?", job.ID).Find(&inputs).Error; err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].FileID != inputID || inputs[0].Position != 0 {
		t.Fatalf("unexpected backfilled inputs: %+v", inputs)
	}

	var outputs []models.JobOutput
	if err := db.Where("job_id = ?", job.ID).Find(&outputs).Error; err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].FileID != outputID || outputs[0].Position != 0 {
		t.Fatalf("unexpected backfilled outputs: %+v", outputs)
	}

	var migrated models.Job
	if err := db.First(&migrated, "id = ?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(migrated.OperationParams) == 0 {
		t.Fatal("legacy params were not copied to operation_params")
	}
}

func TestMigrateUpgradesLegacyVarcharAndEnum(t *testing.T) {
	db := testutil.OpenDatabase(t)
	for _, sql := range []string{
		`ALTER TABLE jobs ALTER COLUMN status DROP DEFAULT`,
		`ALTER TABLE jobs ALTER COLUMN status TYPE varchar(20) USING status::text`,
		`ALTER TABLE jobs ALTER COLUMN status SET DEFAULT 'PENDING'`,
		`DROP TYPE job_status`,
		`CREATE TYPE job_status AS ENUM ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED')`,
	} {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatalf("prepare legacy schema: %v", err)
		}
	}
	job := models.Job{OwnerID: uuid.New(), Status: models.JobFailed}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("upgrade enum and legacy varchar column: %v", err)
	}
	var column struct {
		DataType string
		UDTName  string
	}
	if err := db.Raw(`SELECT data_type, udt_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'jobs' AND column_name = 'status'`).
		Scan(&column).Error; err != nil {
		t.Fatal(err)
	}
	if column.DataType != "USER-DEFINED" || column.UDTName != "job_status" {
		t.Fatalf("status was not converted to enum: %+v", column)
	}
	if err := db.First(&job, "id = ?", job.ID).Error; err != nil || job.Status != models.JobFailed {
		t.Fatalf("legacy status lost: %+v, %v", job, err)
	}
}

func TestConcurrentMigrationsAreSerialized(t *testing.T) {
	db := testutil.OpenDatabase(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := schema.Migrate(db); err != nil {
				t.Errorf("concurrent migration: %v", err)
			}
		})
	}
	wg.Wait()
}
