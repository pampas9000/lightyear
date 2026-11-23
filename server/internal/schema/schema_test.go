package schema_test

import (
	"sync"
	"testing"

	"transcoder/server/internal/models"
	"transcoder/server/internal/schema"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
)

func TestMigrateIsIdempotent(t *testing.T) {
	db := testutil.OpenDatabase(t)
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("second migration: %v", err)
	}
}

func TestResetDropsLegacyTablesAndData(t *testing.T) {
	db := testutil.OpenDatabase(t)
	if err := db.Exec(`CREATE TABLE outbox_events (id uuid PRIMARY KEY)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.User{Username: "u", Email: "u@example.com"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := schema.Reset(db); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable("outbox_events") {
		t.Error("legacy table survived reset")
	}
	var users int64
	if err := db.Model(&models.User{}).Count(&users).Error; err != nil || users != 0 {
		t.Errorf("users after reset = %d, %v", users, err)
	}
	if !db.Migrator().HasTable(&models.Attempt{}) {
		t.Error("schema not recreated")
	}
}

func TestConcurrentMigrationsAreSerialized(t *testing.T) {
	db := testutil.OpenDatabase(t)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() { errs <- schema.Migrate(db) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// A database from before the outbox table recorded sends in
// attempts.dispatched_at. Its never-sent attempts must still be sent.
func TestMigrateMovesUnsentAttemptsIntoTheOutbox(t *testing.T) {
	db := testutil.OpenDatabase(t)
	if err := db.Exec(`ALTER TABLE attempts ADD COLUMN dispatched_at timestamptz`).Error; err != nil {
		t.Fatal(err)
	}
	unsent := models.Attempt{JobID: uuid.New(), Status: models.AttemptQueued, Queue: "q:image", Payload: models.JSON(`{"v":2}`), OutputKey: "a"}
	sent := models.Attempt{JobID: uuid.New(), Status: models.AttemptQueued, Queue: "q:image", Payload: models.JSON(`{"v":2}`), OutputKey: "b"}
	for _, a := range []*models.Attempt{&unsent, &sent} {
		if err := db.Create(a).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`UPDATE attempts SET dispatched_at = now() WHERE id = ?`, sent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := schema.Migrate(db); err != nil {
		t.Fatal(err)
	}
	var msgs []models.OutboxMessage
	if err := db.Find(&msgs).Error; err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || *msgs[0].AttemptID != unsent.ID || msgs[0].SentAt != nil || msgs[0].Queue != "q:image" {
		t.Fatalf("expected only the unsent attempt in the outbox: %+v", msgs)
	}
	if db.Migrator().HasColumn(&models.Attempt{}, "dispatched_at") {
		t.Error("attempts.dispatched_at survived migration")
	}
}

func TestMigrateReplacesTheGlobalDispatchIndex(t *testing.T) {
	db := testutil.OpenDatabase(t)
	if err := db.Exec(`CREATE INDEX idx_jobs_due ON jobs (status, next_run_at)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := schema.Migrate(db); err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := db.Raw(`SELECT indexname FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'jobs'`).Scan(&names).Error; err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	if have["idx_jobs_due"] || !have["idx_jobs_pending_owner"] || !have["idx_jobs_processing_owner"] {
		t.Fatalf("unexpected job indexes: %v", names)
	}
}
