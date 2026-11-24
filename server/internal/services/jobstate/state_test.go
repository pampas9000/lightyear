package jobstate

import (
	"errors"
	"testing"
	"time"

	"transcoder/server/internal/compression"
	"transcoder/server/internal/imageoptions"
	"transcoder/server/internal/models"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type fixture struct {
	t     *testing.T
	db    *gorm.DB
	owner uuid.UUID
}

func setup(t *testing.T) *fixture {
	db := testutil.OpenDatabase(t)
	return &fixture{t: t, db: db, owner: uuid.New()}
}

// newTask creates a task with n fresh jobs and returns their ids.
func (f *fixture) newTask(n int, target *compression.Target) (uuid.UUID, []uuid.UUID) {
	f.t.Helper()
	task := models.Task{OwnerID: f.owner, Status: models.TaskPending}
	var ids []uuid.UUID
	err := Run(f.db, func(t *Tx) error {
		if err := t.CreateTask(&task); err != nil {
			return err
		}
		for i := range n {
			job := models.Job{OwnerID: f.owner, TaskID: task.ID, OutputIndex: i, Operation: models.OpImageConvert,
				Options: imageoptions.Options{Format: "webp"}, Target: target, SourceFileID: uuid.New(), MaxAttempts: 3}
			if err := t.Insert(&job, time.Now()); err != nil {
				return err
			}
			ids = append(ids, job.ID)
		}
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return task.ID, ids
}

// do locks the job (task first) and applies fn in one transaction.
func (f *fixture) do(id uuid.UUID, fn func(*Tx, *models.Job) error) error {
	return Run(f.db, func(t *Tx) error {
		job, err := t.Lock(id)
		if err != nil {
			return err
		}
		return fn(t, job)
	})
}

func (f *fixture) must(id uuid.UUID, fn func(*Tx, *models.Job) error) {
	f.t.Helper()
	if err := f.do(id, fn); err != nil {
		f.t.Fatal(err)
	}
}

// job reloads the job and checks the stored row against the invariants.
func (f *fixture) job(id uuid.UUID) models.Job {
	f.t.Helper()
	var j models.Job
	if err := f.db.First(&j, "id = ?", id).Error; err != nil {
		f.t.Fatal(err)
	}
	if err := Check(j); err != nil {
		f.t.Fatalf("stored row: %v", err)
	}
	return j
}

func (f *fixture) taskStatus(id uuid.UUID) models.TaskStatus {
	f.t.Helper()
	var task models.Task
	if err := f.db.First(&task, "id = ?", id).Error; err != nil {
		f.t.Fatal(err)
	}
	return task.Status
}

func claim(attempt uuid.UUID) func(*Tx, *models.Job) error {
	return func(t *Tx, j *models.Job) error { return t.Claim(j, attempt, time.Now().Add(time.Minute)) }
}

func TestRunOfAJob(t *testing.T) {
	f := setup(t)
	task, ids := f.newTask(1, nil)
	id := ids[0]
	if j := f.job(id); j.Status != models.JobPending || j.NextRunAt == nil || f.taskStatus(task) != models.TaskPending {
		t.Fatalf("inserted: %+v", j)
	}

	first := uuid.New()
	f.must(id, claim(first))
	if j := f.job(id); j.Status != models.JobProcessing || *j.CurrentAttemptID != first || j.Attempts != 1 ||
		j.NextRunAt != nil || f.taskStatus(task) != models.TaskProcessing {
		t.Fatalf("claimed: %+v", j)
	}

	// Heartbeats need only the job, raise progress and never lower it.
	beat := func(p int) {
		t.Helper()
		if err := Run(f.db, func(tx *Tx) error {
			j, err := tx.LockJob(id)
			if err != nil {
				return err
			}
			return tx.Heartbeat(j, &p, time.Now().Add(2*time.Minute))
		}); err != nil {
			t.Fatal(err)
		}
	}
	beat(40)
	beat(10)
	if j := f.job(id); j.Progress != 40 {
		t.Fatalf("progress %d", j.Progress)
	}

	f.must(id, func(tx *Tx, j *models.Job) error {
		return tx.Requeue(j, "storage_failed", "boom", time.Now().Add(time.Second))
	})
	if j := f.job(id); j.Status != models.JobPending || j.CurrentAttemptID != nil || j.Attempts != 1 || j.Progress != 0 ||
		j.LeaseExpiresAt != nil || j.ErrorCode != "storage_failed" || f.taskStatus(task) != models.TaskPending {
		t.Fatalf("requeued: %+v", j)
	}

	second := uuid.New()
	f.must(id, claim(second))
	if j := f.job(id); j.Attempts != 2 || j.ErrorCode != "storage_failed" {
		t.Fatalf("claimed again (keeps the last error): %+v", j)
	}
	out := uuid.New()
	met := true
	f.must(id, func(tx *Tx, j *models.Job) error {
		return tx.Complete(j, Output{FileID: out, Resolved: j.Options, TargetMet: &met})
	})
	if j := f.job(id); j.Status != models.JobCompleted || *j.OutputFileID != out || *j.CurrentAttemptID != second ||
		j.Progress != 100 || j.ErrorCode != "" || j.LeaseExpiresAt != nil || j.TargetMet == nil || f.taskStatus(task) != models.TaskCompleted {
		t.Fatalf("completed: %+v", j)
	}

	f.must(id, func(tx *Tx, j *models.Job) error {
		j.Options = imageoptions.Options{Format: "avif"}
		return tx.Reset(j, time.Now())
	})
	if j := f.job(id); j.Status != models.JobPending || j.Attempts != 0 || j.OutputFileID != nil || j.ResolvedOptions != nil ||
		j.TargetMet != nil || j.CurrentAttemptID != nil || j.Options.Format != "avif" || f.taskStatus(task) != models.TaskPending {
		t.Fatalf("reset: %+v", j)
	}
}

func TestFailAndCancel(t *testing.T) {
	f := setup(t)
	task, ids := f.newTask(3, nil)

	// A PENDING job fails or is cancelled without an attempt.
	f.must(ids[0], func(tx *Tx, j *models.Job) error { return tx.Fail(j, "source_unavailable", "gone") })
	var stopped *uuid.UUID
	f.must(ids[1], func(tx *Tx, j *models.Job) (err error) { stopped, err = tx.Cancel(j); return err })
	if stopped != nil {
		t.Fatalf("cancelling a PENDING job stopped attempt %s", *stopped)
	}
	if j := f.job(ids[0]); j.Status != models.JobFailed || j.NextRunAt != nil || j.CurrentAttemptID != nil {
		t.Fatalf("failed: %+v", j)
	}

	// A PROCESSING job keeps its last attempt as current when it ends.
	attempt := uuid.New()
	f.must(ids[2], claim(attempt))
	f.must(ids[2], func(tx *Tx, j *models.Job) (err error) { stopped, err = tx.Cancel(j); return err })
	if stopped == nil || *stopped != attempt {
		t.Fatalf("stopped %v, want %s", stopped, attempt)
	}
	j := f.job(ids[2])
	if j.Status != models.JobCancelled || *j.CurrentAttemptID != attempt || j.LeaseExpiresAt != nil ||
		j.ErrorCode != CancelCode || j.ErrorMessage != CancelMessage {
		t.Fatalf("cancelled: %+v", j)
	}
	if got := f.taskStatus(task); got != models.TaskFailed {
		t.Fatalf("task %s", got)
	}
}

func TestReuse(t *testing.T) {
	f := setup(t)
	limit := int64(100)
	task, ids := f.newTask(1, &compression.Target{MaxBytes: &limit})
	output := &models.File{Base: models.Base{ID: uuid.New()}, Size: 80}
	f.must(ids[0], func(tx *Tx, j *models.Job) error { return tx.Reuse(j, output, j.Options) })
	j := f.job(ids[0])
	if j.Status != models.JobCompleted || *j.OutputFileID != output.ID || j.CurrentAttemptID != nil || j.Attempts != 0 ||
		j.TargetMet == nil || !*j.TargetMet || j.Achieved == nil || *j.Achieved.Bytes != 80 || f.taskStatus(task) != models.TaskCompleted {
		t.Fatalf("reused: %+v", j)
	}

	// A job that already ran is never completed by reuse.
	_, ids = f.newTask(1, nil)
	f.must(ids[0], claim(uuid.New()))
	f.must(ids[0], func(tx *Tx, j *models.Job) error { return tx.Requeue(j, "storage_failed", "x", time.Now()) })
	if err := f.do(ids[0], func(tx *Tx, j *models.Job) error { return tx.Reuse(j, output, j.Options) }); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reuse after an attempt: %v", err)
	}
}

func TestTransitionsRefuseWrongStatusesAndLocks(t *testing.T) {
	f := setup(t)
	_, ids := f.newTask(1, nil)
	id := ids[0]
	wrong := map[string]func(*Tx, *models.Job) error{
		"complete pending": func(tx *Tx, j *models.Job) error {
			return tx.Complete(j, Output{FileID: uuid.New(), Resolved: j.Options})
		},
		"requeue pending":   func(tx *Tx, j *models.Job) error { return tx.Requeue(j, "x", "x", time.Now()) },
		"heartbeat pending": func(tx *Tx, j *models.Job) error { return tx.Heartbeat(j, nil, time.Now()) },
		"reset pending":     func(tx *Tx, j *models.Job) error { return tx.Reset(j, time.Now()) },
	}
	for name, fn := range wrong {
		if err := f.do(id, fn); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("%s: %v", name, err)
		}
	}

	f.must(id, claim(uuid.New()))
	// Holding only the job allows a heartbeat, nothing that changes the status.
	err := Run(f.db, func(tx *Tx) error {
		j, err := tx.LockJob(id)
		if err != nil {
			return err
		}
		if err := tx.Heartbeat(j, nil, time.Now().Add(time.Minute)); err != nil {
			return err
		}
		return tx.Complete(j, Output{FileID: uuid.New(), Resolved: j.Options})
	})
	if !errors.Is(err, ErrNotLocked) {
		t.Fatalf("complete with only the job locked: %v", err)
	}
	if j := f.job(id); j.Status != models.JobProcessing {
		t.Fatalf("refused transition was written: %s", j.Status)
	}

	// A job read without a lock cannot be changed at all.
	var unlocked models.Job
	f.db.First(&unlocked, "id = ?", id)
	if err := Run(f.db, func(tx *Tx) error { return tx.Heartbeat(&unlocked, nil, time.Now()) }); !errors.Is(err, ErrNotLocked) {
		t.Fatalf("heartbeat without a lock: %v", err)
	}
}

func TestLockTaskLocksEveryJob(t *testing.T) {
	f := setup(t)
	task, ids := f.newTask(2, nil)
	err := Run(f.db, func(tx *Tx) error {
		got, jobs, err := tx.LockTask(f.owner, task)
		if err != nil {
			return err
		}
		if got.ID != task || len(jobs) != len(ids) {
			t.Fatalf("locked %s with %d jobs", got.ID, len(jobs))
		}
		for i := range jobs {
			if _, err := tx.Cancel(&jobs[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.taskStatus(task); got != models.TaskCancelled {
		t.Fatalf("task %s", got)
	}
	if err := Run(f.db, func(tx *Tx) error { _, _, err := tx.LockTask(uuid.New(), task); return err }); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("another owner's task: %v", err)
	}
}

func TestCheck(t *testing.T) {
	now := time.Now()
	attempt, out := uuid.New(), uuid.New()
	opts := imageoptions.Options{Format: "webp"}
	met := true
	valid := map[string]models.Job{
		"pending":          {Status: models.JobPending, NextRunAt: &now},
		"retrying":         {Status: models.JobPending, NextRunAt: &now, Attempts: 1, ErrorCode: "storage_failed"},
		"processing":       {Status: models.JobProcessing, CurrentAttemptID: &attempt, Attempts: 1, LeaseExpiresAt: &now, Progress: 30},
		"completed":        {Status: models.JobCompleted, CurrentAttemptID: &attempt, Attempts: 1, Progress: 100, OutputFileID: &out, ResolvedOptions: &opts, TargetMet: &met},
		"reused":           {Status: models.JobCompleted, Progress: 100, OutputFileID: &out, ResolvedOptions: &opts},
		"failed":           {Status: models.JobFailed, CurrentAttemptID: &attempt, Attempts: 3, Progress: 40, ErrorCode: "x"},
		"cancelled":        {Status: models.JobCancelled, ErrorCode: CancelCode},
		"cancelled midway": {Status: models.JobCancelled, CurrentAttemptID: &attempt, Attempts: 1, Progress: 20, ErrorCode: CancelCode},
	}
	for name, j := range valid {
		if err := Check(j); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	invalid := map[string]models.Job{
		"pending with attempt":       {Status: models.JobPending, NextRunAt: &now, CurrentAttemptID: &attempt},
		"pending not due":            {Status: models.JobPending},
		"pending with output":        {Status: models.JobPending, NextRunAt: &now, OutputFileID: &out},
		"processing without lease":   {Status: models.JobProcessing, CurrentAttemptID: &attempt, Attempts: 1},
		"processing without attempt": {Status: models.JobProcessing, Attempts: 1, LeaseExpiresAt: &now},
		"completed without output":   {Status: models.JobCompleted, Progress: 100, ResolvedOptions: &opts},
		"completed with error":       {Status: models.JobCompleted, Progress: 100, OutputFileID: &out, ResolvedOptions: &opts, ErrorCode: "x"},
		"failed with lease":          {Status: models.JobFailed, ErrorCode: "x", LeaseExpiresAt: &now},
		"failed without error":       {Status: models.JobFailed},
		"cancelled due again":        {Status: models.JobCancelled, ErrorCode: CancelCode, NextRunAt: &now},
		"unknown":                    {Status: "LOST"},
	}
	for name, j := range invalid {
		if err := Check(j); !errors.Is(err, ErrInvariant) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
