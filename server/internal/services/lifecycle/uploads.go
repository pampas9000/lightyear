package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"transcoder/server/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DraftRetention is how long a draft (an uploaded file not yet submitted in a
// task) is kept.
const DraftRetention = 7 * 24 * time.Hour

// UploadSweepResult counts what one SweepUploads did.
type UploadSweepResult struct {
	Recovered int // uploads whose object was there, now UPLOADED
	Abandoned int // uploads that never finished, now DELETED
	Drafts    int // stale drafts removed
	Purged    int // objects removed
	Failed    int // uploads skipped or objects not removed; retried next time
}

// SweepUploads settles uploads whose upload window has run out and removes
// drafts older than DraftRetention. Like Sweep, it handles at most BatchSize
// rows per step and is safe to run concurrently.
func (s *Service) SweepUploads(ctx context.Context, now time.Time) (UploadSweepResult, error) {
	var res UploadSweepResult
	if err := s.abandonUploads(ctx, now, &res); err != nil {
		return res, fmt.Errorf("abandon uploads: %w", err)
	}
	if err := s.expireDrafts(ctx, now, &res); err != nil {
		return res, fmt.Errorf("expire drafts: %w", err)
	}
	if res.Recovered+res.Abandoned+res.Drafts+res.Failed > 0 {
		slog.Info("upload sweep", "recovered", res.Recovered, "abandoned", res.Abandoned,
			"drafts", res.Drafts, "purged", res.Purged, "failed", res.Failed)
	}
	return res, nil
}

// abandonUploads handles UPLOADING files past upload_expires_at. If the
// object is in storage the upload finished but was never completed, so the
// file becomes UPLOADED (and keeps its draft). Otherwise it is deleted. When
// storage cannot tell, the file is left for the next sweep.
func (s *Service) abandonUploads(ctx context.Context, now time.Time, res *UploadSweepResult) error {
	var due []models.File
	if err := s.db.WithContext(ctx).
		Where("status = ? AND upload_expires_at <= ?", models.FileUploading, now).
		Order("upload_expires_at").Limit(s.BatchSize).Find(&due).Error; err != nil {
		return err
	}
	for _, f := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		head, err := s.store.HeadObject(ctx, s.bucket, f.Path)
		switch {
		case err == nil:
			var size int64
			if head.ContentLength != nil {
				size = *head.ContentLength
			}
			if err := s.recordUploaded(ctx, f.ID, size, now); err != nil {
				return err
			}
			res.Recovered++
		case isNotFound(err):
			file, err := s.abandon(ctx, f.ID, now)
			if err != nil {
				return err
			}
			if file == nil {
				continue
			}
			res.Abandoned++
			if s.purge(ctx, *file, now) {
				res.Purged++
			} else {
				res.Failed++
			}
		default:
			slog.Warn("check unfinished upload; will retry", "file_id", f.ID, "path", f.Path, "error", err)
			res.Failed++
		}
	}
	return nil
}

// recordUploaded records an upload whose object reached storage as UPLOADED.
func (s *Service) recordUploaded(ctx context.Context, id uuid.UUID, size int64, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.File{}).Where("id = ? AND status = ?", id, models.FileUploading).
			Updates(map[string]any{"status": models.FileUploaded, "size": size, "uploaded_at": now})
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error
		}
		_, err := Schedule(tx, s.policy, id)
		return err
	})
}

// abandon deletes an upload that is still UPLOADING past its window and
// returns it, or nil if it changed in the meantime.
func (s *Service) abandon(ctx context.Context, id uuid.UUID, now time.Time) (*models.File, error) {
	var file models.File
	marked := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDrafts(tx, id); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&file, "id = ?", id).Error; err != nil {
			return err
		}
		if file.Status != models.FileUploading || file.UploadExpiresAt == nil || file.UploadExpiresAt.After(now) {
			return nil
		}
		var err error
		marked, err = markDeleted(tx, &file, now)
		return err
	})
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && !marked {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &file, nil
}

// expireDrafts removes drafts older than DraftRetention whose file is
// UPLOADED or gone, deleting the file unless a job points at it. Drafts of
// unfinished uploads are left to abandonUploads.
func (s *Service) expireDrafts(ctx context.Context, now time.Time, res *UploadSweepResult) error {
	var stale []models.DraftFile
	if err := s.db.WithContext(ctx).
		Joins("LEFT JOIN files ON files.id = draft_files.file_id").
		Where("draft_files.created_at < ? AND (files.id IS NULL OR files.status = ?)", now.Add(-DraftRetention), models.FileUploaded).
		Order("draft_files.created_at").Limit(s.BatchSize).Find(&stale).Error; err != nil {
		return err
	}
	notUploaded := func(f models.File) bool { return f.Status != models.FileUploaded }
	for _, d := range stale {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var removed bool
		var file *models.File
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var err error
			removed, file, err = dropDraft(tx, d.OwnerID, d.FileID, now, notUploaded)
			return err
		})
		if errors.Is(err, ErrDraftNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if removed {
			res.Drafts++
		}
		if file == nil {
			continue
		}
		if s.purge(ctx, *file, now) {
			res.Purged++
		} else {
			res.Failed++
		}
	}
	return nil
}
