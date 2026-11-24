// Package lifecycle removes files from storage: sources and outputs when their
// retention runs out (Sweep), uploads that were never finished and drafts that
// were never submitted (SweepUploads), and whatever the owner deletes
// (DeleteFile, DeleteDraft, DeleteTask). It is the only code that deletes a
// file.
//
// Every removal has two steps. A transaction first changes the file's status
// (to EXPIRED or DELETED), so nothing reads, reuses or signs it any more; only
// then is the object deleted. A failed object deletion leaves the row with
// purged_at NULL and the next sweep tries again, so the database never claims
// an object that is gone. After the object is gone the row stays while jobs
// still point at it (they show the status) and is deleted otherwise.
//
// A deleted upload that never finished may still receive the browser's PUT
// or parts while its signed URLs are valid, so its object is removed only
// once upload_expires_at, which deletion brings forward to at most
// UploadURLExpiry away, has passed. Its multipart upload is aborted at once
// and again then.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"transcoder/server/internal/models"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrTaskNotFound  = errors.New("task not found")
	ErrFileNotFound  = errors.New("file not found")
	ErrDraftNotFound = errors.New("draft not found")
	// ErrTaskActive: the task has unfinished jobs; cancel it first.
	ErrTaskActive = errors.New("task has unfinished jobs; cancel it before deleting")
	// ErrFileInUse: an unfinished job reads the file as its source.
	ErrFileInUse = errors.New("file is the source of unfinished jobs")
)

// Policy is the global retention. Zero keeps files forever. Users override it
// with users.source_retention_days / output_retention_days.
type Policy struct {
	Source time.Duration
	Output time.Duration
}

// Days builds a Policy from whole days.
func Days(source, output int) Policy {
	return Policy{Source: time.Duration(source) * 24 * time.Hour, Output: time.Duration(output) * 24 * time.Hour}
}

// UploadURLExpiry is how long signed upload and part URLs stay valid. The
// upload service signs with it; deletion waits it out before removing the
// object of an unfinished upload.
const UploadURLExpiry = time.Hour

// ObjectStore is the storage lifecycle needs; storage.S3Service implements it.
type ObjectStore interface {
	DeleteObject(ctx context.Context, bucket, key string) error
	AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error
	HeadObject(ctx context.Context, bucket, key string) (*s3.HeadObjectOutput, error)
}

type Service struct {
	db     *gorm.DB
	store  ObjectStore
	bucket string
	policy Policy
	// BatchSize bounds how many files each sweep step handles.
	BatchSize int
}

func NewService(db *gorm.DB, store ObjectStore, bucket string, policy Policy) *Service {
	return &Service{db: db, store: store, bucket: bucket, policy: policy, BatchSize: 500}
}

// Policy is the retention the service applies.
func (s *Service) Policy() Policy { return s.policy }

// unfinished are the job statuses that still need their source file.
var unfinished = []models.JobStatus{models.JobPending, models.JobProcessing}

// gone are the statuses whose object is (being) removed and whose row is kept.
var gone = []string{models.FileExpired, models.FileDeleted}

// UploadSweepInterval is how often Start runs SweepUploads. Unfinished
// uploads are given a day and drafts a week, so an hour is soon enough.
const UploadSweepInterval = time.Hour

// Start runs Sweep every interval and SweepUploads every UploadSweepInterval
// until ctx is done.
func (s *Service) Start(ctx context.Context, interval time.Duration) {
	slog.Info("storage lifecycle sweep started", "interval", interval, "upload_interval", UploadSweepInterval,
		"source_retention", s.policy.Source, "output_retention", s.policy.Output)
	go every(ctx, interval, func() error {
		_, err := s.Sweep(ctx, time.Now())
		return err
	}, "storage lifecycle sweep")
	go every(ctx, UploadSweepInterval, func() error {
		_, err := s.SweepUploads(ctx, time.Now())
		return err
	}, "upload sweep")
}

// every runs fn now and then every interval until ctx is done.
func every(ctx context.Context, interval time.Duration, fn func() error, name string) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := fn(); err != nil && ctx.Err() == nil {
			slog.Error(name, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SweepResult counts what one Sweep did.
type SweepResult struct {
	Scheduled int64 // files given an expires_at
	Expired   int   // files marked EXPIRED
	Skipped   int   // expired files kept because an unfinished job reads them
	Purged    int   // objects removed
	Failed    int   // object removals that failed and will be retried
}

// Sweep schedules, expires and purges files. It is safe to run concurrently
// (for example from several API instances): every step locks the row and
// re-checks it, and deleting an object twice is harmless.
func (s *Service) Sweep(ctx context.Context, now time.Time) (SweepResult, error) {
	var res SweepResult
	n, err := s.schedule(ctx)
	if err != nil {
		return res, fmt.Errorf("schedule expiry: %w", err)
	}
	res.Scheduled = n
	// Files whose object removal was tried in this sweep are not retried in it.
	tried := map[uuid.UUID]bool{}
	if err := s.expire(ctx, now, &res, tried); err != nil {
		return res, fmt.Errorf("expire files: %w", err)
	}
	if err := s.purgePending(ctx, now, &res, tried); err != nil {
		return res, fmt.Errorf("purge files: %w", err)
	}
	if res.Scheduled+int64(res.Expired+res.Purged+res.Failed) > 0 {
		slog.Info("storage lifecycle sweep", "scheduled", res.Scheduled, "expired", res.Expired,
			"skipped", res.Skipped, "purged", res.Purged, "failed", res.Failed)
	}
	return res, nil
}

// schedule gives every unscheduled UPLOADED or FETCHING file its expires_at.
// Files are normally scheduled as they become UPLOADED (Schedule); this
// catches the rest, such as files uploaded before a retention change, files
// stored by a process that does not know the policy, and URL sources that are
// still waiting to be fetched.
func (s *Service) schedule(ctx context.Context) (int64, error) {
	return Schedule(s.db.WithContext(ctx), s.policy)
}

// expirable are the statuses Schedule and expire handle. A FETCHING file is a
// URL source no worker has fetched yet; if every job reading it ends first
// (all cancelled, say), it would otherwise stay FETCHING forever.
var expirable = []string{models.FileUploaded, models.FileFetching}

// Schedule sets expires_at on the given UPLOADED or FETCHING files that have
// none, or on all such files when ids is empty: uploaded_at + retention for
// UPLOADED, created_at + source retention for FETCHING (it can be retried
// until then). A file that some job produced is an output; any other file is
// a source. Retention 0 (the user's or, without one, the policy's) leaves it
// NULL. Run it in the transaction that marks the file UPLOADED, after the job
// that produced an output points at it.
func Schedule(db *gorm.DB, policy Policy, ids ...uuid.UUID) (int64, error) {
	days := func(d time.Duration) int { return int(d / (24 * time.Hour)) }
	only, args := "", []any{models.FileFetching, days(policy.Output), days(policy.Source), expirable}
	if len(ids) > 0 {
		only, args = " AND f.id IN ?", append(args, ids)
	}
	res := db.Exec(`
		WITH due AS (
			SELECT f.id,
				CASE WHEN f.status = ? THEN f.created_at ELSE f.uploaded_at END AS since,
				CASE WHEN EXISTS (SELECT 1 FROM jobs j WHERE j.output_file_id = f.id)
					THEN COALESCE((SELECT u.output_retention_days FROM users u WHERE u.id = f.owner_id), ?::int)
					ELSE COALESCE((SELECT u.source_retention_days FROM users u WHERE u.id = f.owner_id), ?::int)
				END AS days
			FROM files f
			WHERE f.status IN ? AND f.expires_at IS NULL`+only+`
		)
		UPDATE files SET expires_at = due.since + make_interval(days => due.days::int)
		FROM due
		WHERE files.id = due.id AND due.since IS NOT NULL AND due.days > 0
			AND files.status IN ? AND files.expires_at IS NULL`,
		append(args, expirable)...)
	return res.RowsAffected, res.Error
}

// expire marks UPLOADED and FETCHING files past expires_at as EXPIRED and
// removes their objects (a FETCHING file may have one: a worker can store the
// fetch and stop before reporting it). A file an unfinished job reads is
// skipped until the job ends.
func (s *Service) expire(ctx context.Context, now time.Time, res *SweepResult, tried map[uuid.UUID]bool) error {
	var due []models.File
	if err := s.db.WithContext(ctx).
		Where("status IN ? AND expires_at IS NOT NULL AND expires_at <= ?", expirable, now).
		Order("expires_at").Limit(s.BatchSize).Find(&due).Error; err != nil {
		return err
	}
	for _, f := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var file models.File
		marked := false
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Drafts before files, as task creation locks them.
			if err := lockDrafts(tx, f.ID); err != nil {
				return err
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&file, "id = ?", f.ID).Error; err != nil {
				return err
			}
			if !slices.Contains(expirable, file.Status) || file.ExpiresAt == nil || file.ExpiresAt.After(now) {
				return nil
			}
			busy, err := inUse(tx, file.ID)
			if err != nil || busy {
				return err
			}
			if err := tx.Where("file_id = ?", file.ID).Delete(&models.DraftFile{}).Error; err != nil {
				return err
			}
			marked = true
			return tx.Model(&file).Update("status", models.FileExpired).Error
		})
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			continue
		case err != nil:
			return err
		case !marked:
			res.Skipped++
			continue
		}
		res.Expired++
		file.Status = models.FileExpired
		tried[file.ID] = true
		if s.purge(ctx, file, now) {
			res.Purged++
		} else {
			res.Failed++
		}
	}
	return nil
}

// purgePending retries removing objects of EXPIRED and DELETED files, and
// removes those of deleted uploads whose signed URLs have run out.
func (s *Service) purgePending(ctx context.Context, now time.Time, res *SweepResult, tried map[uuid.UUID]bool) error {
	var pending []models.File
	if err := s.db.WithContext(ctx).
		Where("status IN ? AND purged_at IS NULL", gone).
		Where("uploaded_at IS NOT NULL OR upload_expires_at IS NULL OR upload_expires_at <= ?", now).
		Order("updated_at").Limit(s.BatchSize).Find(&pending).Error; err != nil {
		return err
	}
	for _, f := range pending {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if tried[f.ID] {
			continue
		}
		if s.purge(ctx, f, now) {
			res.Purged++
		} else {
			res.Failed++
		}
	}
	return nil
}

// purge deletes the object of an EXPIRED or DELETED file, then records it:
// the row is deleted when no job points at the file, otherwise purged_at is
// set. It reports whether the object is gone; on failure the row keeps
// purged_at NULL and the next sweep retries.
//
// For an upload that never finished, it first aborts the multipart upload,
// if there is one, and stops there while upload_expires_at is ahead: a
// signed URL could still write the object after it was deleted.
func (s *Service) purge(ctx context.Context, f models.File, now time.Time) bool {
	if f.UploadedAt == nil && f.UploadID != "" {
		err := s.store.AbortMultipartUpload(ctx, s.bucket, f.Path, f.UploadID)
		if err != nil && !isNoSuchUpload(err) {
			slog.Warn("abort multipart upload; will retry", "file_id", f.ID, "path", f.Path, "error", err)
			return false
		}
	}
	if f.UploadedAt == nil && f.UploadExpiresAt != nil && f.UploadExpiresAt.After(now) {
		return false
	}
	if err := s.store.DeleteObject(ctx, s.bucket, f.Path); err != nil && !isNotFound(err) {
		slog.Warn("delete object; will retry", "file_id", f.ID, "path", f.Path, "error", err)
		return false
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDrafts(tx, f.ID); err != nil {
			return err
		}
		var file models.File
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&file, "id = ?", f.ID).Error; err != nil {
			return err
		}
		if !slices.Contains(gone, file.Status) || file.PurgedAt != nil {
			return nil
		}
		referenced, err := referenced(tx, file.ID)
		if err != nil {
			return err
		}
		if referenced {
			return tx.Model(&file).Update("purged_at", now).Error
		}
		if err := tx.Where("file_id = ?", file.ID).Delete(&models.DraftFile{}).Error; err != nil {
			return err
		}
		return tx.Delete(&file).Error
	})
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		// The object is gone; the row is settled on the next sweep.
		slog.Error("record purged object", "file_id", f.ID, "error", err)
	}
	return true
}

func isNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey":
			return true
		}
	}
	return false
}

func isNoSuchUpload(err error) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchUpload"
}

// markDeleted marks the file, which the caller has locked after its drafts,
// DELETED and removes its drafts. An UPLOADED or FETCHING file an unfinished
// job reads returns ErrFileInUse. An unfinished upload's upload_expires_at is
// brought forward to at most UploadURLExpiry from now, when its object can be
// removed. An EXPIRED or DELETED file is left as it is. It reports whether
// the file has an object to purge.
func markDeleted(tx *gorm.DB, file *models.File, now time.Time) (bool, error) {
	updates := map[string]any{"status": models.FileDeleted}
	switch file.Status {
	// A FETCHING file may already have an object: a worker can write it
	// and stop before the fetch is reported.
	case models.FileUploaded, models.FileFetching:
		busy, err := inUse(tx, file.ID)
		if err != nil {
			return false, err
		}
		if busy {
			return false, ErrFileInUse
		}
	case models.FileUploading:
		limit := now.Add(UploadURLExpiry)
		if file.UploadExpiresAt == nil || file.UploadExpiresAt.After(limit) {
			file.UploadExpiresAt = &limit
		}
		updates["upload_expires_at"] = *file.UploadExpiresAt
	default:
		return false, nil
	}
	if err := tx.Where("file_id = ?", file.ID).Delete(&models.DraftFile{}).Error; err != nil {
		return false, err
	}
	if err := tx.Model(file).Updates(updates).Error; err != nil {
		return false, err
	}
	file.Status = models.FileDeleted
	return true, nil
}

func lockDrafts(tx *gorm.DB, fileID uuid.UUID) error {
	var drafts []models.DraftFile
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("file_id = ?", fileID).Find(&drafts).Error
}

// inUse reports whether an unfinished job reads the file. Callers hold the
// file's row lock, which task creation and retry also take on their source
// files, so no new reader can appear before they commit.
func inUse(tx *gorm.DB, fileID uuid.UUID) (bool, error) {
	var n int64
	err := tx.Model(&models.Job{}).
		Where("(source_file_id = ? OR output_file_id = ?) AND status IN ?", fileID, fileID, unfinished).
		Limit(1).Count(&n).Error
	return n > 0, err
}

// referenced reports whether any job points at the file.
func referenced(tx *gorm.DB, fileID uuid.UUID) (bool, error) {
	var n int64
	err := tx.Model(&models.Job{}).
		Where("source_file_id = ? OR output_file_id = ?", fileID, fileID).
		Limit(1).Count(&n).Error
	return n > 0, err
}
