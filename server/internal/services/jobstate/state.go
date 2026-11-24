// Package jobstate owns every change of a job's state. A transition is one
// method of Tx: it checks the job's current status, sets every state field
// the target status defines (see Check), writes them, and marks the task for
// re-aggregation. Callers decide when a transition happens; they never write
// job state columns themselves.
//
// Locking: a transition that changes a job's status needs its task locked in
// the same transaction (Lock, LockTask or CreateTask), always before the job.
// The one exception is the orchestrator's RUNNING heartbeat, which locks only
// the job (LockJob) and may only call Heartbeat: it never changes a status or
// re-aggregates the task.
package jobstate

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"transcoder/server/internal/compression"
	"transcoder/server/internal/imageoptions"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/taskstatus"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrInvalidTransition: the job's status does not allow the transition.
	ErrInvalidTransition = errors.New("invalid job transition")
	// ErrNotLocked: the transition needs a lock this transaction does not hold.
	ErrNotLocked = errors.New("job or task not locked in this transaction")
	// ErrInvariant: a transition would leave the job's fields inconsistent.
	ErrInvariant = errors.New("job state invariant violated")
)

// CancelCode and CancelMessage are the error a cancelled job shows.
const (
	CancelCode    = "cancelled"
	CancelMessage = "Task cancelled by user"
)

// stateColumns are written by every status-changing transition, so no field
// of an earlier state survives into the next.
var stateColumns = []string{
	"status", "current_attempt_id", "attempts", "lease_expires_at", "next_run_at", "progress",
	"error_code", "error_message", "output_file_id", "resolved_options", "target_met", "achieved",
	"updated_at",
}

// Tx is one transaction's view of job state: the locks it holds and the
// tasks whose job statuses changed.
type Tx struct {
	db    *gorm.DB
	tasks map[uuid.UUID]bool
	jobs  map[uuid.UUID]bool
	dirty []uuid.UUID
}

// Run runs fn in a transaction and, if fn succeeds, re-aggregates every task
// whose job statuses fn changed, once, before committing.
func Run(db *gorm.DB, fn func(*Tx) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		t := &Tx{db: tx, tasks: map[uuid.UUID]bool{}, jobs: map[uuid.UUID]bool{}}
		if err := fn(t); err != nil {
			return err
		}
		for _, id := range t.dirty {
			if err := Aggregate(tx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// DB is the transaction, for everything that is not job state.
func (t *Tx) DB() *gorm.DB { return t.db }

// Lock locks the job's task and then the job.
func (t *Tx) Lock(id uuid.UUID) (*models.Job, error) {
	var ref models.Job
	if err := t.db.Select("id", "task_id").First(&ref, "id = ?", id).Error; err != nil {
		return nil, err
	}
	var task models.Task
	if err := t.db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&task, "id = ?", ref.TaskID).Error; err != nil {
		return nil, err
	}
	t.tasks[task.ID] = true
	return t.LockJob(id)
}

// LockJob locks only the job row. It is for RUNNING heartbeats, which arrive
// every 20 seconds for every running job and would otherwise queue on the
// task row behind their siblings. Skipping the task adds no deadlock cycle:
// every other writer takes the task before the job, and every path that
// locks a job and a file takes the job first, so a heartbeat holding only the
// job waits for nothing that waits for it.
func (t *Tx) LockJob(id uuid.UUID) (*models.Job, error) {
	var job models.Job
	if err := t.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, "id = ?", id).Error; err != nil {
		return nil, err
	}
	t.jobs[job.ID] = true
	return &job, nil
}

// LockTask locks the owner's task and then all its jobs in id order. A
// missing task is gorm.ErrRecordNotFound.
func (t *Tx) LockTask(owner, taskID uuid.UUID) (*models.Task, []models.Job, error) {
	var task models.Task
	if err := t.db.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&task, "id = ? AND owner_id = ?", taskID, owner).Error; err != nil {
		return nil, nil, err
	}
	t.tasks[task.ID] = true
	var jobs []models.Job
	if err := t.db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("task_id = ?", task.ID).Order("id, output_index").Find(&jobs).Error; err != nil {
		return nil, nil, err
	}
	for _, j := range jobs {
		t.jobs[j.ID] = true
	}
	return &task, jobs, nil
}

// CreateTask inserts a task. No other transaction can see it before commit,
// so it counts as locked.
func (t *Tx) CreateTask(task *models.Task) error {
	if err := t.db.Create(task).Error; err != nil {
		return err
	}
	t.tasks[task.ID] = true
	return nil
}

// Insert creates job as a fresh PENDING run, due now. The caller sets what
// the job is (owner, task, source, options, target, artifact key, limits).
func (t *Tx) Insert(job *models.Job, now time.Time) error {
	if !t.tasks[job.TaskID] {
		return fmt.Errorf("%w: insert into task %s", ErrNotLocked, job.TaskID)
	}
	fresh(job, now)
	if err := Check(*job); err != nil {
		return err
	}
	if err := t.db.Create(job).Error; err != nil {
		return err
	}
	t.jobs[job.ID] = true
	t.mark(job.TaskID)
	return nil
}

// Claim starts attempt on a PENDING job; the lease bounds how long the
// attempt may take to report RUNNING.
func (t *Tx) Claim(job *models.Job, attempt uuid.UUID, lease time.Time) error {
	if err := t.from(job, true, models.JobPending); err != nil {
		return err
	}
	job.Status, job.CurrentAttemptID, job.Attempts = models.JobProcessing, &attempt, job.Attempts+1
	job.LeaseExpiresAt, job.NextRunAt, job.Progress = &lease, nil, 0
	return t.change(job)
}

// Postpone moves when a PENDING job is next due.
func (t *Tx) Postpone(job *models.Job, at time.Time) error {
	if err := t.from(job, false, models.JobPending); err != nil {
		return err
	}
	job.NextRunAt = &at
	return t.write(job, "next_run_at", "updated_at")
}

// Heartbeat renews a PROCESSING job's lease and raises its progress (nil
// leaves it). It needs only the job lock.
func (t *Tx) Heartbeat(job *models.Job, progress *int, lease time.Time) error {
	if err := t.from(job, false, models.JobProcessing); err != nil {
		return err
	}
	if progress != nil {
		job.Progress = max(job.Progress, min(max(*progress, 0), 100))
	}
	job.LeaseExpiresAt = &lease
	return t.write(job, "progress", "lease_expires_at", "updated_at")
}

// Output is what a completed attempt produced.
type Output struct {
	FileID   uuid.UUID
	Resolved imageoptions.Options
	// TargetMet and Achieved are as the worker reported or the orchestrator
	// derived them; nil when not applicable.
	TargetMet *bool
	Achieved  *compression.Achieved
}

// Complete finishes a PROCESSING job with its current attempt's output.
func (t *Tx) Complete(job *models.Job, out Output) error {
	if err := t.from(job, true, models.JobProcessing); err != nil {
		return err
	}
	resolved := out.Resolved
	job.Status, job.Progress, job.LeaseExpiresAt = models.JobCompleted, 100, nil
	job.OutputFileID, job.ResolvedOptions, job.TargetMet, job.Achieved = &out.FileID, &resolved, out.TargetMet, out.Achieved
	job.ErrorCode, job.ErrorMessage = "", ""
	return t.change(job)
}

// Reuse completes a fresh PENDING job with an existing output instead of
// running it. resolved is what produced the output; target_met and achieved
// are derived from the output's size and this job's target.
func (t *Tx) Reuse(job *models.Job, output *models.File, resolved imageoptions.Options) error {
	if err := t.from(job, true, models.JobPending); err != nil {
		return err
	}
	if job.Attempts != 0 || job.CurrentAttemptID != nil {
		return fmt.Errorf("%w: reuse of job %s that already ran", ErrInvalidTransition, job.ID)
	}
	job.Status, job.Progress, job.NextRunAt = models.JobCompleted, 100, nil
	job.OutputFileID, job.ResolvedOptions = &output.ID, &resolved
	job.TargetMet, job.Achieved = compression.IsTargetMet(output.Size, job.Target), nil
	if job.Target != nil {
		job.Achieved = &compression.Achieved{Bytes: new(output.Size)}
	}
	job.ErrorCode, job.ErrorMessage = "", ""
	return t.change(job)
}

// Requeue returns a PROCESSING job whose attempt failed to PENDING, due at
// at. The failure stays on the job until the run ends; the attempt count is
// kept.
func (t *Tx) Requeue(job *models.Job, code, message string, at time.Time) error {
	if err := t.from(job, true, models.JobProcessing); err != nil {
		return err
	}
	job.Status, job.Progress, job.CurrentAttemptID, job.LeaseExpiresAt, job.NextRunAt = models.JobPending, 0, nil, nil, &at
	job.ErrorCode, job.ErrorMessage = code, message
	return t.change(job)
}

// Fail ends a PENDING or PROCESSING job as FAILED. The last attempt, if any,
// stays current so it can be found.
func (t *Tx) Fail(job *models.Job, code, message string) error {
	if err := t.from(job, true, models.JobPending, models.JobProcessing); err != nil {
		return err
	}
	job.Status, job.LeaseExpiresAt, job.NextRunAt = models.JobFailed, nil, nil
	job.ErrorCode, job.ErrorMessage = code, message
	return t.change(job)
}

// Cancel ends a PENDING or PROCESSING job as CANCELLED and returns the attempt
// it stopped, if any, which the caller marks cancelled and signals.
func (t *Tx) Cancel(job *models.Job) (*uuid.UUID, error) {
	if err := t.from(job, true, models.JobPending, models.JobProcessing); err != nil {
		return nil, err
	}
	var stopped *uuid.UUID
	if job.Status == models.JobProcessing {
		stopped = job.CurrentAttemptID
	}
	job.Status, job.LeaseExpiresAt, job.NextRunAt = models.JobCancelled, nil, nil
	job.ErrorCode, job.ErrorMessage = CancelCode, CancelMessage
	return stopped, t.change(job)
}

// Reset starts a finished job over as a fresh PENDING run, due now, and
// writes its options, target and artifact key, which the caller may have
// changed.
func (t *Tx) Reset(job *models.Job, now time.Time) error {
	if err := t.from(job, true, models.JobCompleted, models.JobFailed, models.JobCancelled); err != nil {
		return err
	}
	fresh(job, now)
	return t.change(job, "options", "target", "artifact_key")
}

// fresh sets the fields of a run that has not started.
func fresh(job *models.Job, now time.Time) {
	job.Status, job.Progress, job.Attempts, job.NextRunAt = models.JobPending, 0, 0, &now
	job.CurrentAttemptID, job.LeaseExpiresAt, job.OutputFileID = nil, nil, nil
	job.ResolvedOptions, job.TargetMet, job.Achieved = nil, nil, nil
	job.ErrorCode, job.ErrorMessage = "", ""
}

// from checks that the transaction holds the locks a transition needs and
// that the job is in one of the allowed statuses.
func (t *Tx) from(job *models.Job, changesStatus bool, allowed ...models.JobStatus) error {
	if !t.jobs[job.ID] || (changesStatus && !t.tasks[job.TaskID]) {
		return fmt.Errorf("%w: job %s", ErrNotLocked, job.ID)
	}
	if !slices.Contains(allowed, job.Status) {
		return fmt.Errorf("%w: job %s is %s", ErrInvalidTransition, job.ID, job.Status)
	}
	return nil
}

func (t *Tx) change(job *models.Job, extra ...string) error {
	if err := Check(*job); err != nil {
		return err
	}
	if err := t.write(job, append(slices.Clone(stateColumns), extra...)...); err != nil {
		return err
	}
	t.mark(job.TaskID)
	return nil
}

func (t *Tx) write(job *models.Job, columns ...string) error {
	job.UpdatedAt = time.Now()
	return t.db.Model(job).Select(columns).Updates(job).Error
}

func (t *Tx) mark(taskID uuid.UUID) {
	if !slices.Contains(t.dirty, taskID) {
		t.dirty = append(t.dirty, taskID)
	}
}

// Aggregate sets the task's status from its jobs' statuses. Run calls it for
// every task it changed; it is exported for test fixtures that write job
// statuses directly.
func Aggregate(tx *gorm.DB, taskID uuid.UUID) error {
	var statuses []models.JobStatus
	if err := tx.Model(&models.Job{}).Where("task_id = ?", taskID).Pluck("status", &statuses).Error; err != nil {
		return err
	}
	return tx.Model(&models.Task{}).Where("id = ?", taskID).Update("status", taskstatus.Aggregate(statuses)).Error
}

func Terminal(status models.JobStatus) bool {
	return status == models.JobCompleted || status == models.JobFailed || status == models.JobCancelled
}

// Check reports whether the job's fields are consistent with its status:
//
//	field                PENDING        PROCESSING   COMPLETED          FAILED      CANCELLED
//	current_attempt_id   NULL           set          completing / NULL  last / NULL last / NULL
//	attempts             >= 0           >= 1         >= 0               >= 0        >= 0
//	lease_expires_at     NULL           set          NULL               NULL        NULL
//	next_run_at          set            NULL         NULL               NULL        NULL
//	progress             0              0..100       100                as left     as left
//	error_code/message   last retry's   carried      empty              set         cancelled
//	output_file_id       NULL           NULL         set                NULL        NULL
//	resolved_options     NULL           NULL         set                NULL        NULL
//	target_met/achieved  NULL           NULL         as derived         NULL        NULL
//
// A completed job without a current attempt was completed by reuse.
func Check(job models.Job) error {
	bad := func(what string) error {
		return fmt.Errorf("%w: %s job %s: %s", ErrInvariant, job.Status, job.ID, what)
	}
	if job.Attempts < 0 || job.Progress < 0 || job.Progress > 100 {
		return bad("attempts or progress out of range")
	}
	if job.Status != models.JobCompleted &&
		(job.OutputFileID != nil || job.ResolvedOptions != nil || job.TargetMet != nil || job.Achieved != nil) {
		return bad("output fields set")
	}
	if (job.Status == models.JobProcessing) != (job.LeaseExpiresAt != nil) {
		return bad("lease_expires_at set iff PROCESSING")
	}
	if (job.Status == models.JobPending) != (job.NextRunAt != nil) {
		return bad("next_run_at set iff PENDING")
	}
	switch job.Status {
	case models.JobPending:
		if job.CurrentAttemptID != nil || job.Progress != 0 {
			return bad("current attempt or progress set")
		}
	case models.JobProcessing:
		if job.CurrentAttemptID == nil || job.Attempts < 1 {
			return bad("no current attempt")
		}
	case models.JobCompleted:
		if job.Progress != 100 || job.OutputFileID == nil || job.ResolvedOptions == nil || job.ErrorCode != "" || job.ErrorMessage != "" {
			return bad("output, progress or error inconsistent")
		}
	case models.JobFailed:
		if job.ErrorCode == "" {
			return bad("no error code")
		}
	case models.JobCancelled:
		if job.ErrorCode != CancelCode {
			return bad("error code is not " + CancelCode)
		}
	default:
		return bad("unknown status")
	}
	return nil
}
