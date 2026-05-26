package storage

import (
	"context"
	"log/slog"
	"time"

	"transcoder/server/internal/models"

	"gorm.io/gorm"
)

// StartCleanupTask runs a background worker that periodically deletes expired/stale file upload records from the database.
func StartCleanupTask(ctx context.Context, db *gorm.DB, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		slog.Info("stale files cleanup task started", "interval", interval)

		// Run once immediately on start
		runCleanup(ctx, db)

		for {
			select {
			case <-ctx.Done():
				slog.Info("stale files cleanup task stopped")
				return
			case <-ticker.C:
				runCleanup(ctx, db)
			}
		}
	}()
}

func runCleanup(ctx context.Context, db *gorm.DB) {
	now := time.Now()
	slog.Debug("running cleanup of stale file records", "time", now)

	// Delete records where status is UPLOADING or FAILED and the upload session has expired
	result := db.WithContext(ctx).
		Where("(status = ? OR status = ?) AND upload_expires_at IS NOT NULL AND upload_expires_at < ?", "UPLOADING", "FAILED", now).
		Delete(&models.File{})

	if result.Error != nil {
		slog.Error("failed to cleanup stale file records", "error", result.Error)
	} else if result.RowsAffected > 0 {
		slog.Info("cleaned up stale file records", "count", result.RowsAffected)
	}
}
