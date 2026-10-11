package schema_test

import (
	"sync"
	"testing"

	"transcoder/server/internal/models"
	"transcoder/server/internal/schema"
	"transcoder/server/internal/testutil"
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
