package lifecycle

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/jobstate"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TaskDeletion reports what DeleteTask did with the task's outputs.
type TaskDeletion struct {
	ID uuid.UUID `json:"id"`
	// DeletedOutputs are outputs only this task used; they are removed.
	DeletedOutputs []uuid.UUID `json:"deleted_outputs"`
	// RetainedOutputs are outputs other tasks reuse; they stay until those
	// tasks are deleted or the outputs expire.
	RetainedOutputs []uuid.UUID `json:"retained_outputs"`
}

// DeleteTask deletes a finished task, its jobs and attempts, and the outputs
// no other task's job uses. Shared outputs are reference counted by their
// jobs: an output goes when the last job pointing at it does. Source files
// are not touched (delete them with DeleteFile). A task with unfinished jobs
// returns ErrTaskActive.
func (s *Service) DeleteTask(ctx context.Context, owner, taskID uuid.UUID) (*TaskDeletion, error) {
	out := &TaskDeletion{ID: taskID, DeletedOutputs: []uuid.UUID{}, RetainedOutputs: []uuid.UUID{}}
	var purge []models.File
	err := jobstate.Run(s.db.WithContext(ctx), func(st *jobstate.Tx) error {
		tx := st.DB()
		t, jobs, err := st.LockTask(owner, taskID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		jobIDs := make([]uuid.UUID, 0, len(jobs))
		var outputs []uuid.UUID
		for _, j := range jobs {
			if !jobstate.Terminal(j.Status) {
				return ErrTaskActive
			}
			jobIDs = append(jobIDs, j.ID)
			if j.OutputFileID != nil && !slices.Contains(outputs, *j.OutputFileID) {
				outputs = append(outputs, *j.OutputFileID)
			}
		}
		if len(jobIDs) > 0 {
			if err := tx.Where("attempt_id IN (?)", tx.Model(&models.Attempt{}).Select("id").Where("job_id IN ?", jobIDs)).
				Delete(&models.OutboxMessage{}).Error; err != nil {
				return err
			}
			if err := tx.Where("job_id IN ?", jobIDs).Delete(&models.Attempt{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", jobIDs).Delete(&models.Job{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Delete(t).Error; err != nil {
			return err
		}
		// Lock outputs in a fixed order. Task creation locks a reused output
		// before pointing a new job at it, so once we hold the lock the count
		// below includes every job that can still use it.
		slices.SortFunc(outputs, func(a, b uuid.UUID) int { return cmp.Compare(a.String(), b.String()) })
		for _, id := range outputs {
			var f models.File
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&f, "id = ?", id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return err
			}
			shared, err := referenced(tx, f.ID)
			if err != nil {
				return err
			}
			if shared {
				out.RetainedOutputs = append(out.RetainedOutputs, f.ID)
				continue
			}
			out.DeletedOutputs = append(out.DeletedOutputs, f.ID)
			switch {
			case f.Status == models.FileUploaded:
				if err := tx.Model(&f).Update("status", models.FileDeleted).Error; err != nil {
					return err
				}
				f.Status = models.FileDeleted
				purge = append(purge, f)
			case slices.Contains(gone, f.Status) && f.PurgedAt == nil:
				purge = append(purge, f)
			case slices.Contains(gone, f.Status):
				// Object already removed; nothing points at the row any more.
				if err := tx.Delete(&f).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.purgeNow(ctx, purge...)
	return out, nil
}

// purgeBudget bounds how long a delete request waits for object removal.
// What is not removed in time stays pending and the sweep retries it.
const purgeBudget = 5 * time.Second

// purgeNow removes the objects of files a request just deleted. It does not
// follow the request's cancellation, so a client that disconnects does not
// cut a removal short, and it gives up after purgeBudget.
func (s *Service) purgeNow(ctx context.Context, files ...models.File) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), purgeBudget)
	defer cancel()
	now := time.Now()
	for _, f := range files {
		if ctx.Err() != nil {
			return
		}
		s.purge(ctx, f, now)
	}
}

// DeleteFile deletes one of the owner's files, a source or an output. An
// unfinished upload is deleted too: its multipart upload is aborted now and
// its object removed once its signed URLs run out. A file an unfinished job
// reads returns ErrFileInUse. Jobs that used the file keep pointing at it and
// show it as DELETED. Deleting a file that is already expired or deleted
// succeeds.
func (s *Service) DeleteFile(ctx context.Context, owner, fileID uuid.UUID) (*models.File, error) {
	var file models.File
	purge := false
	now := time.Now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("file_id = ? AND owner_id = ?", fileID, owner).Find(&[]models.DraftFile{}).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&file, "id = ? AND owner_id = ?", fileID, owner).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrFileNotFound
			}
			return err
		}
		var err error
		purge, err = markDeleted(tx, &file, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	if purge {
		s.purgeNow(ctx, file)
	}
	return &file, nil
}

// DeleteDraft removes one of the owner's drafts. Its file is deleted too
// unless a job points at it: a draft that was submitted, or whose file
// another task uses, only stops being a draft. It reports whether the file
// was deleted. A draft whose file is gone is removed.
func (s *Service) DeleteDraft(ctx context.Context, owner, fileID uuid.UUID) (bool, error) {
	var file *models.File
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		_, file, err = dropDraft(tx, owner, fileID, time.Now(), nil)
		return err
	})
	if err != nil {
		return false, err
	}
	if file != nil {
		s.purgeNow(ctx, *file)
	}
	return file != nil, nil
}

// dropDraft removes the owner's draft of the file and deletes the file unless
// a job points at it. If keep is set and returns true for the file, nothing
// changes. It reports whether the draft was removed, and returns the file if
// it was deleted and has an object to purge.
func dropDraft(tx *gorm.DB, owner, fileID uuid.UUID, now time.Time, keep func(models.File) bool) (bool, *models.File, error) {
	var draft models.DraftFile
	// Drafts before files, as task creation locks them.
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&draft, "file_id = ? AND owner_id = ?", fileID, owner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil, ErrDraftNotFound
		}
		return false, nil, err
	}
	var file models.File
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&file, "id = ?", fileID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil, tx.Delete(&draft).Error
	}
	if err != nil {
		return false, nil, err
	}
	if keep != nil && keep(file) {
		return false, nil, nil
	}
	if err := tx.Delete(&draft).Error; err != nil {
		return false, nil, err
	}
	used, err := referenced(tx, file.ID)
	if err != nil || used {
		return err == nil, nil, err
	}
	purge, err := markDeleted(tx, &file, now)
	if err != nil {
		return false, nil, err
	}
	if !purge {
		return true, nil, nil
	}
	return true, &file, nil
}
