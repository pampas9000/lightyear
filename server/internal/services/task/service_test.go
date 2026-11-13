package task

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"

	"transcoder/server/internal/compression"
	"transcoder/server/internal/imageoptions"
	"transcoder/server/internal/models"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func uploadedFile(t *testing.T, db *gorm.DB, owner uuid.UUID) models.File {
	t.Helper()
	file := models.File{OwnerID: owner, Name: "source.png", Path: "uploads/" + uuid.NewString() + ".png", Status: "UPLOADED"}
	if err := db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	return file
}

func avif(quality int) json.RawMessage {
	return json.RawMessage(`{"format":"avif","quality":` + strconv.Itoa(quality) + `}`)
}

func TestCreateTaskStoresCanonicalOptionsAndIsIdempotent(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	file := uploadedFile(t, db, owner)
	s := NewService(db, nil)
	input := CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: file.ID}}, Options: avif(70), IdempotencyKey: "one"}
	got, err := s.CreateTask(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	job := got.Jobs[0]
	if len(got.Jobs) != 1 || job.Status != models.JobPending || job.SourceFileID != file.ID || job.Operation != models.OpImageConvert {
		t.Fatalf("unexpected job: %+v", job)
	}
	if job.Options.Effort != nil || job.Options.Metadata != imageoptions.MetadataStrip {
		t.Fatalf("options were not canonicalized: %+v", job.Options)
	}
	if job.ArtifactKey == "" {
		t.Fatal("artifact key not set")
	}
	repeated, err := s.CreateTask(t.Context(), input)
	if err != nil || repeated.ID != got.ID {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	input.Options = avif(71)
	if _, err := s.CreateTask(t.Context(), input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected conflict for different options, got %v", err)
	}
}

func TestCreateTaskRejectsInvalidRequests(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	file := uploadedFile(t, db, owner)
	s := NewService(db, nil)
	items := []TaskItemInput{{FileID: file.ID}}
	for name, input := range map[string]CreateTaskInput{
		"no items":         {OwnerID: owner, Options: avif(70)},
		"no options":       {OwnerID: owner, Items: items},
		"invalid options":  {OwnerID: owner, Items: items, Options: json.RawMessage(`{"format":"avif","icc_path":"/etc/passwd"}`)},
		"foreign file":     {OwnerID: uuid.New(), Items: items, Options: avif(70)},
		"options+workflow": {OwnerID: owner, Items: items, Options: avif(70), WorkflowID: new(uuid.New())},
		"missing workflow": {OwnerID: owner, Items: items, WorkflowID: new(uuid.New())},
	} {
		if _, err := s.CreateTask(t.Context(), input); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestCompletedArtifactIsReusedForSameOwnerOnly(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	file := uploadedFile(t, db, owner)
	s := NewService(db, nil)
	first, err := s.CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: file.ID}}, Options: avif(70)})
	if err != nil {
		t.Fatal(err)
	}
	output := models.File{OwnerID: owner, Name: "source.avif", Path: "outputs/x.avif", Status: "UPLOADED"}
	db.Create(&output)
	db.Model(&models.Job{}).Where("id = ?", first.Jobs[0].ID).
		Updates(map[string]any{"status": models.JobCompleted, "output_file_id": output.ID})

	again, err := s.CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: file.ID}},
		Options: json.RawMessage(`{"quality":70,"format":"avif","metadata":"strip"}`)})
	if err != nil {
		t.Fatal(err)
	}
	job := again.Jobs[0]
	if job.Status != models.JobCompleted || job.OutputFileID == nil || *job.OutputFileID != output.ID || again.Status != models.TaskCompleted {
		t.Fatalf("equal intent should reuse the artifact: %+v", job)
	}
	other, err := s.CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: file.ID}}, Options: avif(50)})
	if err != nil || other.Jobs[0].Status != models.JobPending {
		t.Fatalf("different options must not reuse: %v %+v", err, other)
	}
}

func TestWorkflowOptionsAreUsed(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	file := uploadedFile(t, db, owner)
	options, _ := imageoptions.Parse([]byte(`{"format":"webp","quality":42}`))
	wf := models.Workflow{OwnerID: owner, Name: "thumbs", Outputs: compression.WorkflowOutputs{{Options: options}}}
	db.Create(&wf)
	got, err := NewService(db, nil).CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, WorkflowID: &wf.ID, Items: []TaskItemInput{{FileID: file.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Jobs[0].Options.Format != imageoptions.FormatWebP || *got.Jobs[0].Options.Quality != 42 {
		t.Fatalf("workflow options not applied: %+v", got.Jobs[0].Options)
	}
}

func TestConcurrentIdempotentCreationCreatesOneTask(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	file := uploadedFile(t, db, owner)
	s := NewService(db, nil)
	input := CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: file.ID}}, Options: avif(70), IdempotencyKey: "same"}
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 8)
	for range 8 {
		wg.Go(func() {
			got, err := s.CreateTask(t.Context(), input)
			if err == nil {
				ids <- got.ID
			}
		})
	}
	wg.Wait()
	close(ids)
	seen := map[uuid.UUID]bool{}
	for id := range ids {
		seen[id] = true
	}
	var tasks int64
	db.Model(&models.Task{}).Where("owner_id = ?", owner).Count(&tasks)
	if tasks != 1 || len(seen) != 1 {
		t.Fatalf("expected one task, got %d rows and %d ids", tasks, len(seen))
	}
}

func TestRetryResetsTerminalJobsAndCanReplaceOptions(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	file := uploadedFile(t, db, owner)
	s := NewService(db, nil)
	created, err := s.CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: file.ID}}, Options: avif(70)})
	if err != nil {
		t.Fatal(err)
	}
	job := created.Jobs[0]
	if _, err := s.RetryTask(t.Context(), RetryTaskInput{OwnerID: owner, TaskID: created.ID, JobIDs: []uuid.UUID{job.ID}}); !errors.Is(err, ErrJobNotRetryable) {
		t.Fatalf("active job must not be retried: %v", err)
	}
	if _, err := s.RetryTask(t.Context(), RetryTaskInput{OwnerID: uuid.New(), TaskID: created.ID}); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("foreign owner must not retry: %v", err)
	}
	db.Model(&models.Job{}).Where("id = ?", job.ID).
		Updates(map[string]any{"status": models.JobFailed, "attempts": 3, "error_code": "processing_failed"})
	if _, err := s.RetryTask(t.Context(), RetryTaskInput{OwnerID: owner, TaskID: created.ID,
		Options: json.RawMessage(`{"format":"jxl"}`)}); !errors.Is(err, ErrRetrySingleJob) {
		t.Fatalf("modifying options without single job_id must fail with ErrRetrySingleJob, got %v", err)
	}
	retried, err := s.RetryTask(t.Context(), RetryTaskInput{OwnerID: owner, TaskID: created.ID,
		JobIDs:  []uuid.UUID{job.ID},
		Options: json.RawMessage(`{"format":"jxl"}`)})
	if err != nil {
		t.Fatal(err)
	}
	got := retried.Jobs[0]
	if got.Status != models.JobPending || got.Attempts != 0 || got.ErrorCode != "" || got.Options.Format != imageoptions.FormatJXL || got.ArtifactKey == job.ArtifactKey {
		t.Fatalf("retry did not reset the job: %+v", got)
	}
}

type fakeQueue struct{ cancelled []string }

func (q *fakeQueue) Send(_ context.Context, _ string, _ []byte) error { return nil }
func (q *fakeQueue) Cancel(_ context.Context, id string) error {
	q.cancelled = append(q.cancelled, id)
	return nil
}

func TestCancelMarksJobsAndSignalsRunningAttempts(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	file := uploadedFile(t, db, owner)
	queue := &fakeQueue{}
	s := &Service{db: db, queue: queue}
	created, err := s.CreateTask(t.Context(), CreateTaskInput{OwnerID: owner, Items: []TaskItemInput{{FileID: file.ID}}, Options: avif(70)})
	if err != nil {
		t.Fatal(err)
	}
	attempt := models.Attempt{JobID: created.Jobs[0].ID, Status: models.AttemptRunning, Queue: "q", Payload: models.JSON(`{}`), OutputKey: "k"}
	db.Create(&attempt)
	db.Model(&models.Job{}).Where("id = ?", created.Jobs[0].ID).
		Updates(map[string]any{"status": models.JobProcessing, "current_attempt_id": attempt.ID})
	cancelled, err := s.CancelTask(t.Context(), owner, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != models.TaskCancelled || cancelled.Jobs[0].Status != models.JobCancelled {
		t.Fatalf("task not cancelled: %+v", cancelled)
	}
	if len(queue.cancelled) != 1 || queue.cancelled[0] != attempt.ID.String() {
		t.Fatalf("running attempt not signalled: %v", queue.cancelled)
	}
}

func TestMultiOutputTargetReuseAndListPreload(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	src := models.File{OwnerID: owner, Name: "hero.png", Path: "uploads/" + uuid.NewString() + ".png", Size: 200000, Status: "UPLOADED"}
	if err := db.Create(&src).Error; err != nil {
		t.Fatal(err)
	}
	s := NewService(db, nil)

	// Top-level target combined with outputs must be rejected.
	if _, err := s.CreateTask(t.Context(), CreateTaskInput{
		OwnerID: owner,
		Items:   []TaskItemInput{{FileID: src.ID}},
		Outputs: json.RawMessage(`[{"name":"preview","options":{"format":"avif"}}]`),
		Target:  json.RawMessage(`{"max_bytes":50000}`),
	}); !errors.Is(err, ErrTargetNotAllowed) {
		t.Fatalf("expected ErrTargetNotAllowed, got %v", err)
	}

	// PNG + target rejected.
	if _, err := s.CreateTask(t.Context(), CreateTaskInput{
		OwnerID: owner,
		Items:   []TaskItemInput{{FileID: src.ID}},
		Options: json.RawMessage(`{"format":"png"}`),
		Target:  json.RawMessage(`{"max_bytes":50000}`),
	}); !errors.Is(err, compression.ErrInvalidTarget) {
		t.Fatalf("expected ErrInvalidTarget for png, got %v", err)
	}

	// Multi-output task creation.
	created, err := s.CreateTask(t.Context(), CreateTaskInput{
		OwnerID: owner,
		Items:   []TaskItemInput{{FileID: src.ID}},
		Outputs: json.RawMessage(`[
			{"name":"preview","options":{"format":"avif"},"target":{"max_bytes":100000}},
			{"options":{"format":"webp","quality":80}}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(created.Jobs))
	}
	if created.Jobs[0].OutputName != "preview" || created.Jobs[0].OutputIndex != 0 || created.Jobs[0].Options.Quality != nil {
		t.Fatalf("job 0 unexpected: %+v", created.Jobs[0])
	}
	if created.Jobs[1].OutputName != "" || created.Jobs[1].OutputIndex != 1 || *created.Jobs[1].Options.Quality != 80 {
		t.Fatalf("job 1 unexpected: %+v", created.Jobs[1])
	}

	// Complete first job with resolved_options & oversized output, then reuse it.
	outFile := models.File{OwnerID: owner, Name: "hero.preview.avif", Path: "outputs/p.avif", Size: 250000, Status: "UPLOADED"}
	if err := db.Create(&outFile).Error; err != nil {
		t.Fatal(err)
	}
	resolved, _ := imageoptions.Parse([]byte(`{"format":"avif","quality":45}`))
	db.Model(&models.Job{}).Where("id = ?", created.Jobs[0].ID).Updates(map[string]any{
		"status":           models.JobCompleted,
		"progress":         100,
		"output_file_id":   outFile.ID,
		"resolved_options": resolved,
		"target_met":       false,
		"achieved":         compression.Achieved{Bytes: new(int64(250000))},
	})

	// ListTasks must preload OutputFile and populate compression + resolved_options.
	listed, _, err := s.ListTasks(t.Context(), owner, ListTasksParams{Page: 1, Count: 10})
	if err != nil || len(listed) != 1 || listed[0].Jobs[0].Compression == nil || !listed[0].Jobs[0].Compression.LargerThanSource {
		t.Fatalf("ListTasks missing compression/LargerThanSource: err=%v listed=%+v", err, listed)
	}

	// Reusing the same (options, target) copies resolved_options and recomputes target_met.
	reused, err := s.CreateTask(t.Context(), CreateTaskInput{
		OwnerID: owner,
		Items:   []TaskItemInput{{FileID: src.ID}},
		Options: json.RawMessage(`{"format":"avif"}`),
		Target:  json.RawMessage(`{"max_bytes":100000}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	rj := reused.Jobs[0]
	if rj.Status != models.JobCompleted || rj.ResolvedOptions == nil || *rj.ResolvedOptions.Quality != 45 ||
		rj.TargetMet == nil || *rj.TargetMet || rj.Compression == nil || !rj.Compression.LargerThanSource {
		t.Fatalf("reused job unexpected: %+v", rj)
	}

	// Fixed quality + looser target reuses existing non-target / check-only artifact and evaluates new target_met.
	fixedTask, err := s.CreateTask(t.Context(), CreateTaskInput{
		OwnerID: owner,
		Items:   []TaskItemInput{{FileID: src.ID}},
		Options: json.RawMessage(`{"format":"webp","quality":80}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	webpOut := models.File{OwnerID: owner, Name: "hero.webp", Path: "outputs/w.webp", Size: 80000, Status: "UPLOADED"}
	db.Create(&webpOut)
	db.Model(&models.Job{}).Where("id = ?", fixedTask.Jobs[0].ID).Updates(map[string]any{
		"status": models.JobCompleted, "progress": 100, "output_file_id": webpOut.ID,
	})
	checkOnly, err := s.CreateTask(t.Context(), CreateTaskInput{
		OwnerID: owner,
		Items:   []TaskItemInput{{FileID: src.ID}},
		Options: json.RawMessage(`{"format":"webp","quality":80}`),
		Target:  json.RawMessage(`{"max_bytes":90000}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if checkOnly.Jobs[0].Status != models.JobCompleted || checkOnly.Jobs[0].TargetMet == nil || !*checkOnly.Jobs[0].TargetMet {
		t.Fatalf("check-only target reuse failed: %+v", checkOnly.Jobs[0])
	}

	// Retry updating target & clearing target via explicit null.
	retriedTarget, err := s.RetryTask(t.Context(), RetryTaskInput{
		OwnerID: owner,
		TaskID:  reused.ID,
		JobIDs:  []uuid.UUID{rj.ID},
		Target:  json.RawMessage(`{"max_bytes":300000}`),
	})
	if err != nil || retriedTarget.Jobs[0].Status != models.JobPending || retriedTarget.Jobs[0].Target == nil || *retriedTarget.Jobs[0].Target.MaxBytes != 300000 {
		t.Fatalf("retry updating target failed: err=%v job=%+v", err, retriedTarget)
	}
	db.Model(&models.Job{}).Where("id = ?", rj.ID).Update("status", models.JobFailed)
	clearedTarget, err := s.RetryTask(t.Context(), RetryTaskInput{
		OwnerID: owner,
		TaskID:  reused.ID,
		JobIDs:  []uuid.UUID{rj.ID},
		Target:  json.RawMessage(`null`),
	})
	if err != nil || clearedTarget.Jobs[0].Target != nil || clearedTarget.Jobs[0].Options.Quality == nil {
		t.Fatalf("clearing target on retry should restore default quality: err=%v job=%+v", err, clearedTarget)
	}

	// Adding a target when retrying a previously non-target job (without passing options)
	// must drop the previously defaulted quality so worker search triggers.
	nonTarget, err := s.CreateTask(t.Context(), CreateTaskInput{
		OwnerID: owner,
		Items:   []TaskItemInput{{FileID: src.ID}},
		Options: json.RawMessage(`{"format":"avif"}`),
	})
	if err != nil || nonTarget.Jobs[0].Options.Quality == nil {
		t.Fatalf("expected non-target job with default quality: err=%v", err)
	}
	ntJob := nonTarget.Jobs[0]
	db.Model(&models.Job{}).Where("id = ?", ntJob.ID).Update("status", models.JobFailed)
	addedTarget, err := s.RetryTask(t.Context(), RetryTaskInput{
		OwnerID: owner,
		TaskID:  nonTarget.ID,
		JobIDs:  []uuid.UUID{ntJob.ID},
		Target:  json.RawMessage(`{"max_bytes":50000}`),
	})
	if err != nil {
		t.Fatalf("retry adding target failed: %v", err)
	}
	gotAdded := addedTarget.Jobs[0]
	if gotAdded.Options.Quality != nil || gotAdded.Options.Distance != nil || !compression.NeedsSearch(gotAdded.Options, gotAdded.Target) {
		t.Fatalf("expected adding target on retry to clear quality and enable search: %+v", gotAdded.Options)
	}
}
