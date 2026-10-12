package storage

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"transcoder/server/internal/models"

	"github.com/aws/smithy-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// StartCleanupTask runs a background worker that periodically deletes expired/stale file upload records from the database.
func StartCleanupTask(ctx context.Context, db *gorm.DB, s3 *S3Service, bucket string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		slog.Info("stale files cleanup task started", "interval", interval)

		// Run once immediately on start
		runCleanup(ctx, db, s3, bucket)

		for {
			select {
			case <-ctx.Done():
				slog.Info("stale files cleanup task stopped")
				return
			case <-ticker.C:
				runCleanup(ctx, db, s3, bucket)
			}
		}
	}()
}

func runCleanup(ctx context.Context, db *gorm.DB, s3 *S3Service, bucket string) {
	now := time.Now()
	slog.Debug("running cleanup of stale file records", "time", now)

	// 1. Find expired UPLOADING, FAILED, and ABORTED files
	var expiredUploads []models.File
	err := db.WithContext(ctx).
		Where("(status = ? OR status = ? OR status = ?) AND upload_expires_at IS NOT NULL AND upload_expires_at < ?", "UPLOADING", "FAILED", "ABORTED", now).
		Find(&expiredUploads).Error
	if err != nil {
		slog.Error("failed to find expired uploads for cleanup", "error", err)
	} else {
		for _, file := range expiredUploads {
			// Lock row for update first
			var lockedFile models.File
			txErr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", file.ID).First(&lockedFile).Error; err != nil {
					return err
				}
				// Verify it's still cleanable
				if lockedFile.Status != "UPLOADING" && lockedFile.Status != "FAILED" && lockedFile.Status != "ABORTED" {
					return errors.New("file status changed")
				}
				lockedFile.Status = "ABORTING"
				return tx.Save(&lockedFile).Error
			})
			if txErr != nil {
				slog.Debug("failed to lock expired file, skipping", "id", file.ID, "error", txErr)
				continue
			}

			// Perform S3 operation outside transaction
			shouldDeleteDB := true
			if lockedFile.UploadID != "" {
				slog.Info("aborting expired multipart upload", "path", lockedFile.Path, "upload_id", lockedFile.UploadID)
				abortErr := s3.AbortMultipartUpload(ctx, bucket, lockedFile.Path, lockedFile.UploadID)
				if abortErr != nil {
					var apiErr smithy.APIError
					if errors.As(abortErr, &apiErr) && apiErr.ErrorCode() == "NoSuchUpload" {
						// Multipart upload does not exist, let's verify if the object exists in S3
						shouldDeleteDB = healOrDiscard(ctx, db, s3, bucket, lockedFile, now)
					} else {
						// General abort error, log and skip DB deletion for retry later
						slog.Error("failed to abort multipart upload, keeping DB record", "path", lockedFile.Path, "error", abortErr)
						shouldDeleteDB = false
						_ = db.Model(&lockedFile).Update("status", "UPLOADING")
					}
				}
			} else {
				// A single PUT has no upload to abort; the object exists if the
				// browser finished the PUT but never called complete.
				shouldDeleteDB = healOrDiscard(ctx, db, s3, bucket, lockedFile, now)
			}

			if shouldDeleteDB {
				_ = db.WithContext(ctx).Where("file_id = ?", lockedFile.ID).Delete(&models.DraftFile{})
				if err := db.WithContext(ctx).Delete(&lockedFile).Error; err != nil {
					slog.Error("failed to delete expired upload file record", "id", lockedFile.ID, "error", err)
				}
			}
		}
	}

	// 2. Clean up unsubmitted UPLOADED files (7 days retention, based on draft_files creation)
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)
	var staleDrafts []models.DraftFile
	staleErr := db.WithContext(ctx).
		Where("created_at < ?", sevenDaysAgo).
		Find(&staleDrafts).Error
	if staleErr != nil {
		slog.Error("failed to find stale draft files for cleanup", "error", staleErr)
	} else {
		for _, draft := range staleDrafts {
			var lockedDraft models.DraftFile
			var lockedFile models.File
			shouldDeletePhysical := true
			txErr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", draft.ID).First(&lockedDraft).Error; err != nil {
					return err
				}
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", draft.FileID).First(&lockedFile).Error; err != nil {
					return err
				}
				// Verify it's still UPLOADED
				if lockedFile.Status != "UPLOADED" {
					return errors.New("file status changed")
				}
				referenced, err := IsFileReferenced(tx, lockedFile.ID)
				if err != nil {
					return err
				}
				if referenced {
					shouldDeletePhysical = false
					return tx.Delete(&lockedDraft).Error
				}
				lockedFile.Status = "DELETING"
				return tx.Save(&lockedFile).Error
			})
			if txErr != nil {
				slog.Debug("failed to lock stale file draft, skipping", "id", draft.ID, "error", txErr)
				continue
			}
			if !shouldDeletePhysical {
				continue
			}

			// Delete from S3 outside transaction
			slog.Info("cleaning up stale unsubmitted draft file", "id", lockedFile.ID, "path", lockedFile.Path, "uploaded_at", lockedFile.UploadedAt)
			if s3Err := s3.DeleteObject(ctx, bucket, lockedFile.Path); s3Err != nil {
				slog.Error("failed to delete stale s3 object during cleanup", "path", lockedFile.Path, "error", s3Err)
				// Revert status back to UPLOADED so it can be retried
				_ = db.Model(&lockedFile).Update("status", "UPLOADED")
				continue
			}

			// Final DB deletion of both draft and file records
			delErr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Where("file_id = ?", lockedFile.ID).Delete(&models.DraftFile{}).Error; err != nil {
					return err
				}
				return tx.Delete(&lockedFile).Error
			})
			if delErr != nil {
				slog.Error("failed to delete stale draft and file records", "id", lockedFile.ID, "error", delErr)
			}
		}
	}

	// 3. Clean up files marked as DELETING or ABORTING
	var filesToDelete []models.File
	deleteErr := db.WithContext(ctx).
		Where("status = ? OR status = ?", "DELETING", "ABORTING").
		Find(&filesToDelete).Error
	if deleteErr != nil {
		slog.Error("failed to find files for deletion cleanup", "error", deleteErr)
	} else {
		for _, file := range filesToDelete {
			var lockedFile models.File
			txErr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", file.ID).First(&lockedFile).Error; err != nil {
					return err
				}
				// Verify it's still cleanable
				if lockedFile.Status != "DELETING" && lockedFile.Status != "ABORTING" {
					return errors.New("file status changed")
				}
				return nil
			})
			if txErr != nil {
				slog.Debug("failed to lock file for deletion, skipping", "id", file.ID, "error", txErr)
				continue
			}

			// Perform S3 operation outside transaction
			shouldDeleteDB := true
			if lockedFile.Status == "ABORTING" && lockedFile.UploadID != "" {
				slog.Info("aborting multipart upload during deletion", "path", lockedFile.Path, "upload_id", lockedFile.UploadID)
				abortErr := s3.AbortMultipartUpload(ctx, bucket, lockedFile.Path, lockedFile.UploadID)
				if abortErr != nil {
					var apiErr smithy.APIError
					if errors.As(abortErr, &apiErr) && apiErr.ErrorCode() == "NoSuchUpload" {
						// Multipart upload doesn't exist, try to delete object just in case it was completed
						_ = s3.DeleteObject(ctx, bucket, lockedFile.Path)
					} else {
						slog.Error("failed to abort multipart upload during cleanup, keeping DB record", "path", lockedFile.Path, "error", abortErr)
						shouldDeleteDB = false
					}
				}
			} else {
				slog.Info("deleting object from S3 during deletion", "path", lockedFile.Path)
				delS3Err := s3.DeleteObject(ctx, bucket, lockedFile.Path)
				if delS3Err != nil {
					var apiErr smithy.APIError
					if errors.As(delS3Err, &apiErr) && (apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchKey") {
						slog.Info("object not found on S3, proceeding to delete DB row", "path", lockedFile.Path)
					} else {
						slog.Error("failed to delete S3 object during cleanup, keeping DB record", "path", lockedFile.Path, "error", delS3Err)
						shouldDeleteDB = false
					}
				}
			}

			if shouldDeleteDB {
				delDBErr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					if err := tx.Where("file_id = ?", lockedFile.ID).Delete(&models.DraftFile{}).Error; err != nil {
						return err
					}
					return tx.Delete(&lockedFile).Error
				})
				if delDBErr != nil {
					slog.Error("failed to physically delete file and draft records during cleanup", "id", lockedFile.ID, "error", delDBErr)
				} else {
					slog.Info("successfully cleaned up file and draft records", "id", lockedFile.ID, "path", lockedFile.Path)
				}
			}
		}
	}
}

// healOrDiscard settles an expired upload whose multipart upload is gone (or
// that never had one). If the object exists the upload finished but complete
// never recorded it, so the row is healed to UPLOADED. It reports whether the
// row should be deleted.
func healOrDiscard(ctx context.Context, db *gorm.DB, s3 *S3Service, bucket string, lockedFile models.File, now time.Time) bool {
	headOut, headErr := s3.HeadObject(ctx, bucket, lockedFile.Path)
	if headErr == nil {
		slog.Info("healing completed upload status", "path", lockedFile.Path)
		healSize := int64(0)
		if headOut.ContentLength != nil {
			healSize = *headOut.ContentLength
		}
		dbErr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var innerFile models.File
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", lockedFile.ID).First(&innerFile).Error; err != nil {
				return err
			}
			innerFile.Status = "UPLOADED"
			innerFile.Size = healSize
			innerFile.UploadedAt = &now
			return tx.Save(&innerFile).Error
		})
		if dbErr != nil {
			slog.Error("failed to heal state in DB", "id", lockedFile.ID, "error", dbErr)
		}
		return false
	}
	// NotFound means the upload never finished, so the row can go.
	var apiErr smithy.APIError
	if errors.As(headErr, &apiErr) && apiErr.ErrorCode() == "NotFound" {
		slog.Info("no completed object found for expired upload, deleting DB row", "path", lockedFile.Path)
		return true
	}
	// S3 temporarily unreachable: keep the row and retry on the next run.
	slog.Warn("failed to check head object, keeping DB record", "path", lockedFile.Path, "error", headErr)
	_ = db.Model(&lockedFile).Update("status", "UPLOADING")
	return false
}
