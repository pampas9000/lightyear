package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func setupConcurrencyTestDB(t *testing.T) *gorm.DB {
	return testutil.OpenDatabase(t)
}

func TestCreateTaskAndCleanupConcurrencyLock(t *testing.T) {
	db := setupConcurrencyTestDB(t)

	// Create user
	user := models.User{
		Username:     "concurrency_user_" + uuid.New().String()[:8],
		Email:        "concurrency_" + uuid.New().String()[:8] + "@example.com",
		PasswordHash: "hash",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create a file in UPLOADED status
	now := time.Now()
	file := models.File{
		OwnerID:    user.ID,
		Name:       "lock_test.mp4",
		Path:       "uploads/" + user.ID.String() + "/lock_test.mp4",
		Status:     models.FileUploaded,
		UploadedAt: &now,
	}
	if err := db.Create(&file).Error; err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	db.Create(&models.DraftFile{
		FileID:  file.ID,
		OwnerID: user.ID,
	})

	// We want to verify that row lock (FOR UPDATE) prevents concurrent modification/cleanup
	// We will start a transaction in Goroutine A that locks the draft file row then the file row, sleeps to hold the lock,
	// and then creates a job referencing this file and deletes the draft_file (simulating task creation).
	// In Goroutine B, we delete the draft through lifecycle, which would delete the file too.
	// Goroutine B should block until Goroutine A commits. Once Goroutine A commits, the draft is gone.

	var wg sync.WaitGroup
	wg.Add(2)

	var goroutineAStart = make(chan struct{})
	var goroutineALocked = make(chan struct{})

	var taskCreatedResult error
	var cleanupResult error
	var bBlockedDuration time.Duration

	// Goroutine A: Starts transaction, locks DraftFile then File, holds it, deletes draft and creates job, commits
	go func() {
		defer wg.Done()
		<-goroutineAStart

		err := db.Transaction(func(tx *gorm.DB) error {
			// Lock draft row
			var d models.DraftFile
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("file_id = ?", file.ID).First(&d).Error; err != nil {
				return err
			}
			// Lock file row
			var f models.File
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ? AND status = ?", file.ID, user.ID, models.FileUploaded).First(&f).Error; err != nil {
				return err
			}

			close(goroutineALocked)             // Notify B that lock is held
			time.Sleep(1500 * time.Millisecond) // Hold the lock for 1.5s

			// Delete draft
			if err := tx.Delete(&d).Error; err != nil {
				return err
			}

			// Create a job referencing the file (simulates task creation)
			job := models.Job{
				OwnerID:      user.ID,
				Status:       models.JobPending,
				SourceFileID: file.ID,
			}
			return tx.Create(&job).Error
		})
		taskCreatedResult = err
	}()

	// Goroutine B: deletes the draft, as the console or the stale-draft sweep does
	go func() {
		defer wg.Done()
		goroutineAStart <- struct{}{} // Start A
		<-goroutineALocked            // Wait until A locks the row

		time.Sleep(100 * time.Millisecond) // Ensure B calls SELECT FOR UPDATE after A has acquired lock

		bStart := time.Now()
		// Locks the draft, then the file: blocks behind A.
		_, cleanupResult = lifecycle.NewService(db, nil, "bucket", lifecycle.Policy{}).
			DeleteDraft(t.Context(), user.ID, file.ID)
		bBlockedDuration = time.Since(bStart)
	}()

	wg.Wait()

	// Assertions
	if taskCreatedResult != nil {
		t.Errorf("expected transaction A to succeed, got: %v", taskCreatedResult)
	}

	// Goroutine B must have blocked for at least 1 second (approaching the remaining 1.4 seconds of hold)
	if bBlockedDuration < 1*time.Second {
		t.Errorf("expected Goroutine B to block on row lock for at least 1s, but only blocked for %v", bBlockedDuration)
	}

	// A submitted the file and removed the draft, so B finds no draft and
	// leaves the file alone.
	if !errors.Is(cleanupResult, lifecycle.ErrDraftNotFound) {
		t.Errorf("expected the draft to be gone once A committed, got: %v", cleanupResult)
	}
	var after models.File
	if err := db.First(&after, "id = ?", file.ID).Error; err != nil || after.Status != models.FileUploaded {
		t.Errorf("file after B: %+v %v", after, err)
	}

	// Cleanup database
	var dbJobs []models.Job
	db.Where("owner_id = ?", user.ID).Find(&dbJobs)
	for _, j := range dbJobs {
		db.Delete(&j)
	}
	db.Where("file_id = ?", file.ID).Delete(&models.DraftFile{})
	db.Delete(&file)
	db.Delete(&user)
}
