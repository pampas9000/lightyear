// Package lifecycle removes source files and outputs from storage: when their
// retention runs out (Sweep) and when the owner deletes a file or a task
// (DeleteFile, DeleteTask).
//
// Every removal has two steps. A transaction first changes the file's status
// (to EXPIRED or DELETED), so nothing reads, reuses or signs it any more; only
// then is the object deleted. A failed object deletion leaves the row with
// purged_at NULL and the next sweep tries again, so the database never claims
// an object that is gone. After the object is gone the row stays while jobs
// still point at it (they show the status) and is deleted otherwise.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"transcoder/server/internal/models"

	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrTaskNotFound = errors.New("task not found")
	ErrFileNotFound = errors.New("file not found")
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

// ObjectStore deletes objects; storage.S3Service implements it.
type ObjectStore interface {
	DeleteObject(ctx context.Context, bucket, key string) error
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

// unfinished are the job statuses that still need their source file.
var unfinished = []models.JobStatus{models.JobPending, models.JobProcessing}

// gone are the statuses whose object is (being) removed and whose row is kept.
var gone = []string{models.FileExpired, models.FileDeleted}

// Start runs Sweep every interval until ctx is done.
func (s *Service) Start(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		slog.Info("storage lifecycle sweep started", "interval", interval,
			"source_retention", s.policy.Source, "output_retention", s.policy.Output)
		for {
			if _, err := s.Sweep(ctx, time.Now()); err != nil && ctx.Err() == nil {
				slog.Error("storage lifecycle sweep", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
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

// schedule gives every unscheduled UPLOADED file its expires_at. Files are
// normally scheduled as they become UPLOADED (Schedule); this catches the
// rest, such as files uploaded before a retention change or by a process
// that does not know the policy.
func (s *Service) schedule(ctx context.Context) (int64, error) {
	return Schedule(s.db.WithContext(ctx), s.policy)
}

// Schedule sets expires_at = uploaded_at + retention on the given UPLOADED
// files that have none, or on all such files when ids is empty. A file that
// some job produced is an output; any other file is a source. Retention 0
// (the user's or, without one, the policy's) leaves it NULL. Run it in the
// transaction that marks the file UPLOADED, after the job that produced an
// output points at it.
func Schedule(db *gorm.DB, policy Policy, ids ...uuid.UUID) (int64, error) {
	days := func(d time.Duration) int { return int(d / (24 * time.Hour)) }
	only, args := "", []any{days(policy.Output), days(policy.Source), models.FileUploaded}
	if len(ids) > 0 {
		only, args = " AND f.id IN ?", append(args, ids)
	}
	res := db.Exec(`
		WITH due AS (
			SELECT f.id, f.uploaded_at,
				CASE WHEN EXISTS (SELECT 1 FROM jobs j WHERE j.output_file_id = f.id)
					THEN COALESCE((SELECT u.output_retention_days FROM users u WHERE u.id = f.owner_id), ?::int)
					ELSE COALESCE((SELECT u.source_retention_days FROM users u WHERE u.id = f.owner_id), ?::int)
				END AS days
			FROM files f
			WHERE f.status = ? AND f.expires_at IS NULL AND f.uploaded_at IS NOT NULL`+only+`
		)
		UPDATE files SET expires_at = due.uploaded_at + make_interval(days => due.days::int)
		FROM due
		WHERE files.id = due.id AND due.days > 0 AND files.status = ? AND files.expires_at IS NULL`,
		append(args, models.FileUploaded)...)
	return res.RowsAffected, res.Error
}

// expire marks UPLOADED files past expires_at as EXPIRED and removes their
// objects. A file an unfinished job reads is skipped until the job ends.
func (s *Service) expire(ctx context.Context, now time.Time, res *SweepResult, tried map[uuid.UUID]bool) error {
	var due []models.File
	if err := s.db.WithContext(ctx).
		Where("status = ? AND expires_at IS NOT NULL AND expires_at <= ?", models.FileUploaded, now).
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
			if file.Status != models.FileUploaded || file.ExpiresAt == nil || file.ExpiresAt.After(now) {
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

// purgePending retries removing objects of EXPIRED and DELETED files.
func (s *Service) purgePending(ctx context.Context, now time.Time, res *SweepResult, tried map[uuid.UUID]bool) error {
	var pending []models.File
	if err := s.db.WithContext(ctx).
		Where("status IN ? AND purged_at IS NULL", gone).
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
func (s *Service) purge(ctx context.Context, f models.File, now time.Time) bool {
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
