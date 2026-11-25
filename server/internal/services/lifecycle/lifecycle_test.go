package lifecycle

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/jobstate"
	"transcoder/server/internal/services/task"
	"transcoder/server/internal/testutil"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// fakeStore records deleted keys and aborted uploads, holds the keys of
// objects HeadObject finds, and fails while fail is set.
type fakeStore struct {
	mu      sync.Mutex
	fail    bool
	deleted []string
	aborted []string
	objects map[string]int64
	calls   int
}

func (f *fakeStore) DeleteObject(_ context.Context, _, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail {
		return errors.New("store unavailable")
	}
	f.deleted = append(f.deleted, key)
	delete(f.objects, key)
	return nil
}

func (f *fakeStore) AbortMultipartUpload(_ context.Context, _, _, uploadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("store unavailable")
	}
	again := slices.Contains(f.aborted, uploadID)
	f.aborted = append(f.aborted, uploadID)
	if again {
		return &smithy.GenericAPIError{Code: "NoSuchUpload"}
	}
	return nil
}

func (f *fakeStore) HeadObject(_ context.Context, _, key string) (*s3.HeadObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, errors.New("store unavailable")
	}
	size, ok := f.objects[key]
	if !ok {
		return nil, &smithy.GenericAPIError{Code: "NotFound"}
	}
	return &s3.HeadObjectOutput{ContentLength: &size}, nil
}

func (f *fakeStore) put(key string, size int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.objects == nil {
		f.objects = map[string]int64{}
	}
	f.objects[key] = size
}

func (f *fakeStore) abortCount(uploadID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, id := range f.aborted {
		if id == uploadID {
			n++
		}
	}
	return n
}

func (f *fakeStore) has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.deleted, key)
}

type env struct {
	t     *testing.T
	db    *gorm.DB
	store *fakeStore
	svc   *Service
	tasks *task.Service
	owner uuid.UUID
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testutil.OpenDatabase(t)
	store := &fakeStore{}
	return &env{t: t, db: db, store: store, svc: NewService(db, store, "bucket", Days(30, 30)),
		tasks: task.NewService(db, nil), owner: uuid.New()}
}

func (e *env) source(name string) models.File {
	e.t.Helper()
	now := time.Now()
	f := models.File{OwnerID: e.owner, Name: name, Path: "uploads/" + e.owner.String() + "/" + uuid.NewString() + ".png",
		Status: models.FileUploaded, Size: 1000, UploadedAt: &now}
	if err := e.db.Create(&f).Error; err != nil {
		e.t.Fatal(err)
	}
	return f
}

func (e *env) submit(src uuid.UUID, quality int) *models.Task {
	e.t.Helper()
	t, err := e.tasks.CreateTask(e.t.Context(), task.CreateTaskInput{
		OwnerID: e.owner, Items: []task.TaskItemInput{{FileID: src}},
		Options: []byte(`{"format":"webp","quality":` + itoa(quality) + `}`),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return t
}

func itoa(n int) string { return strconv.Itoa(n) }

// complete finishes the task's pending jobs with a new output, as the
// orchestrator does.
func (e *env) complete(taskID uuid.UUID) models.File {
	e.t.Helper()
	now := time.Now()
	out := models.File{OwnerID: e.owner, Name: "a.webp", Path: "outputs/" + e.owner.String() + "/" + uuid.NewString() + ".webp",
		Status: models.FileUploaded, Size: 400, MimeType: "image/webp", UploadedAt: &now}
	if err := e.db.Create(&out).Error; err != nil {
		e.t.Fatal(err)
	}
	if err := e.db.Model(&models.Job{}).Where("task_id = ? AND status = ?", taskID, models.JobPending).
		Updates(map[string]any{"status": models.JobCompleted, "progress": 100, "output_file_id": out.ID, "next_run_at": nil,
			"resolved_options": gorm.Expr("options")}).Error; err != nil {
		e.t.Fatal(err)
	}
	if err := jobstate.Aggregate(e.db, taskID); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) file(id uuid.UUID) (models.File, bool) {
	e.t.Helper()
	var f models.File
	err := e.db.First(&f, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return f, false
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return f, true
}

func (e *env) setExpiry(id uuid.UUID, at time.Time) {
	e.t.Helper()
	if err := e.db.Model(&models.File{}).Where("id = ?", id).Update("expires_at", at).Error; err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) sweep() SweepResult {
	e.t.Helper()
	res, err := e.svc.Sweep(e.t.Context(), time.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) job(taskID uuid.UUID) models.Job {
	e.t.Helper()
	t, err := e.tasks.GetTask(e.t.Context(), e.owner, taskID)
	if err != nil {
		e.t.Fatal(err)
	}
	return t.Jobs[0]
}

func TestScheduleUsesRetentionAndUserOverride(t *testing.T) {
	e := setup(t)
	src := e.source("a.png")
	out := e.complete(e.submit(src.ID, 70).ID)
	e.sweep()
	got, _ := e.file(src.ID)
	if got.ExpiresAt == nil || !got.ExpiresAt.Round(time.Second).Equal(got.UploadedAt.Add(30*24*time.Hour).Round(time.Second)) {
		t.Fatalf("source expires_at = %v, uploaded_at %v", got.ExpiresAt, got.UploadedAt)
	}

	// A user's own retention wins; 0 keeps files forever.
	user := models.User{Username: "u", Email: "u@example.com", SourceRetentionDays: new(0), OutputRetentionDays: new(2)}
	user.ID = uuid.New()
	if err := e.db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	e.owner = user.ID
	src2 := e.source("b.png")
	out2 := e.complete(e.submit(src2.ID, 70).ID)
	e.sweep()
	if got, _ := e.file(src2.ID); got.ExpiresAt != nil {
		t.Fatalf("retention 0 scheduled expiry %v", got.ExpiresAt)
	}
	got2, _ := e.file(out2.ID)
	if got2.ExpiresAt == nil || got2.ExpiresAt.Sub(*got2.UploadedAt).Round(time.Hour) != 48*time.Hour {
		t.Fatalf("output with 2-day override expires at %v", got2.ExpiresAt)
	}
	if got, _ := e.file(out.ID); got.ExpiresAt == nil {
		t.Fatal("default-policy output not scheduled")
	}
}

func TestExpiredOutputIsPurgedAndNotReused(t *testing.T) {
	e := setup(t)
	src := e.source("a.png")
	first := e.submit(src.ID, 70)
	out := e.complete(first.ID)

	// Fresh outputs are reused.
	if j := e.job(e.submit(src.ID, 70).ID); j.Status != models.JobCompleted || *j.OutputFileID != out.ID {
		t.Fatalf("fresh output not reused: %s", j.Status)
	}

	e.setExpiry(out.ID, time.Now().Add(-time.Minute))
	res := e.sweep()
	if res.Expired != 1 || res.Purged != 1 {
		t.Fatalf("sweep %+v", res)
	}
	got, ok := e.file(out.ID)
	if !ok || got.Status != models.FileExpired || got.PurgedAt == nil {
		t.Fatalf("output after expiry: ok=%v %+v", ok, got)
	}
	if !e.store.has(out.Path) {
		t.Fatal("object not deleted")
	}
	// The job record stays and shows the expired output.
	if j := e.job(first.ID); j.Status != models.JobCompleted || j.OutputFile == nil || j.OutputFile.Status != models.FileExpired {
		t.Fatalf("job after expiry: %+v", j)
	}
	// The same request now produces a new output instead of reusing it.
	if j := e.job(e.submit(src.ID, 70).ID); j.Status != models.JobPending || j.OutputFileID != nil {
		t.Fatalf("expired output reused: %s %v", j.Status, j.OutputFileID)
	}
}

func TestOutputNearExpiryIsNotReused(t *testing.T) {
	e := setup(t)
	src := e.source("a.png")
	out := e.complete(e.submit(src.ID, 70).ID)
	// Less than half of its 30-day retention left.
	if err := e.db.Model(&models.File{}).Where("id = ?", out.ID).Updates(map[string]any{
		"uploaded_at": time.Now().Add(-20 * 24 * time.Hour), "expires_at": time.Now().Add(10 * 24 * time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if j := e.job(e.submit(src.ID, 70).ID); j.Status != models.JobPending {
		t.Fatalf("output with 10 of 30 days left reused: %s", j.Status)
	}
}

func TestSourceOfUnfinishedJobIsKept(t *testing.T) {
	e := setup(t)
	src := e.source("a.png")
	tk := e.submit(src.ID, 70) // PENDING
	e.setExpiry(src.ID, time.Now().Add(-time.Minute))

	res := e.sweep()
	if res.Skipped != 1 || res.Expired != 0 {
		t.Fatalf("sweep %+v", res)
	}
	if got, _ := e.file(src.ID); got.Status != models.FileUploaded || e.store.calls != 0 {
		t.Fatalf("source of a pending job expired: %s, %d deletes", got.Status, e.store.calls)
	}
	if _, err := e.svc.DeleteFile(t.Context(), e.owner, src.ID); !errors.Is(err, ErrFileInUse) {
		t.Fatalf("DeleteFile on a busy source: %v", err)
	}

	// Once the job ends, the source goes, and retrying the job is refused.
	if _, err := e.tasks.CancelTask(t.Context(), e.owner, tk.ID); err != nil {
		t.Fatal(err)
	}
	if res := e.sweep(); res.Expired != 1 {
		t.Fatalf("sweep after cancel %+v", res)
	}
	// Nothing else points at the source except the job, so the row stays.
	if got, ok := e.file(src.ID); !ok || got.Status != models.FileExpired || !e.store.has(src.Path) {
		t.Fatalf("source after job ended: ok=%v %s", ok, got.Status)
	}
	_, err := e.tasks.RetryTask(t.Context(), task.RetryTaskInput{OwnerID: e.owner, TaskID: tk.ID, RetryAll: true})
	if !errors.Is(err, task.ErrSourceUnavailable) {
		t.Fatalf("retry with expired source: %v", err)
	}
}

// A URL source whose jobs all end before any worker fetches it must not stay
// FETCHING forever: it expires at created_at + source retention, and can be
// retried until then.
func TestUnfetchedURLSourceExpires(t *testing.T) {
	e := setup(t)
	tk, err := e.tasks.CreateTask(t.Context(), task.CreateTaskInput{OwnerID: e.owner,
		Items: []task.TaskItemInput{{URL: "https://example.com/a.png"}}, Options: []byte(`{"format":"webp"}`)})
	if err != nil {
		t.Fatal(err)
	}
	src := *tk.Jobs[0].SourceFile

	// Scheduled from created_at, like any source.
	e.sweep()
	got, _ := e.file(src.ID)
	if got.Status != models.FileFetching || got.ExpiresAt == nil ||
		!got.ExpiresAt.Round(time.Second).Equal(got.CreatedAt.Add(30*24*time.Hour).Round(time.Second)) {
		t.Fatalf("FETCHING source: %s, expires_at %v, created_at %v", got.Status, got.ExpiresAt, got.CreatedAt)
	}

	// While its job is pending it is kept, even past expiry.
	e.setExpiry(src.ID, time.Now().Add(-time.Minute))
	if res := e.sweep(); res.Skipped != 1 || res.Expired != 0 {
		t.Fatalf("sweep with a pending job %+v", res)
	}

	// Cancelled before any fetch: still retryable before expiry.
	if _, err := e.tasks.CancelTask(t.Context(), e.owner, tk.ID); err != nil {
		t.Fatal(err)
	}
	e.setExpiry(src.ID, time.Now().Add(time.Hour))
	if res := e.sweep(); res.Expired != 0 {
		t.Fatalf("expired before its time %+v", res)
	}
	if _, err := e.tasks.RetryTask(t.Context(), task.RetryTaskInput{OwnerID: e.owner, TaskID: tk.ID, RetryAll: true}); err != nil {
		t.Fatalf("retry before expiry: %v", err)
	}
	if _, err := e.tasks.CancelTask(t.Context(), e.owner, tk.ID); err != nil {
		t.Fatal(err)
	}

	// Past expiry with no unfinished job: EXPIRED, any stray object removed,
	// the row kept for the job, and a retry is refused.
	e.setExpiry(src.ID, time.Now().Add(-time.Minute))
	if res := e.sweep(); res.Expired != 1 || res.Purged != 1 {
		t.Fatalf("sweep after cancel %+v", res)
	}
	if got, ok := e.file(src.ID); !ok || got.Status != models.FileExpired || got.PurgedAt == nil || !e.store.has(src.Path) {
		t.Fatalf("URL source after expiry: ok=%v %+v", ok, got)
	}
	_, err = e.tasks.RetryTask(t.Context(), task.RetryTaskInput{OwnerID: e.owner, TaskID: tk.ID, RetryAll: true})
	if !errors.Is(err, task.ErrSourceUnavailable) {
		t.Fatalf("retry with expired URL source: %v", err)
	}
}

// With source retention 0 an unfetched URL source is kept, like an upload.
func TestUnfetchedURLSourceKeptWithoutRetention(t *testing.T) {
	e := setup(t)
	e.svc = NewService(e.db, e.store, "bucket", Days(0, 30))
	tk, err := e.tasks.CreateTask(t.Context(), task.CreateTaskInput{OwnerID: e.owner,
		Items: []task.TaskItemInput{{URL: "https://example.com/a.png"}}, Options: []byte(`{"format":"webp"}`)})
	if err != nil {
		t.Fatal(err)
	}
	e.sweep()
	if got, _ := e.file(tk.Jobs[0].SourceFile.ID); got.Status != models.FileFetching || got.ExpiresAt != nil {
		t.Fatalf("retention 0: %s, expires_at %v", got.Status, got.ExpiresAt)
	}
}

func TestFailedObjectDeletionIsRetried(t *testing.T) {
	e := setup(t)
	src := e.source("a.png")
	out := e.complete(e.submit(src.ID, 70).ID)
	e.setExpiry(out.ID, time.Now().Add(-time.Minute))

	e.store.fail = true
	res := e.sweep()
	if res.Expired != 1 || res.Failed != 1 || res.Purged != 0 {
		t.Fatalf("sweep with failing store %+v", res)
	}
	// The database already says EXPIRED (never "UPLOADED but gone"); the
	// removal is pending.
	if got, _ := e.file(out.ID); got.Status != models.FileExpired || got.PurgedAt != nil {
		t.Fatalf("after failed delete: %s purged_at=%v", got.Status, got.PurgedAt)
	}
	if res := e.sweep(); res.Failed != 1 {
		t.Fatalf("second failing sweep %+v", res)
	}

	e.store.fail = false
	if res := e.sweep(); res.Purged != 1 || res.Failed != 0 {
		t.Fatalf("recovering sweep %+v", res)
	}
	if got, _ := e.file(out.ID); got.PurgedAt == nil || !e.store.has(out.Path) {
		t.Fatalf("not purged after recovery: %+v", got)
	}
	if res := e.sweep(); res.Purged != 0 || res.Expired != 0 {
		t.Fatalf("idle sweep did work: %+v", res)
	}

	// An unreferenced file's row goes once its object is gone.
	loose := e.source("loose.png")
	e.store.fail = true
	if f, err := e.svc.DeleteFile(t.Context(), e.owner, loose.ID); err != nil || f.Status != models.FileDeleted {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, ok := e.file(loose.ID); !ok {
		t.Fatal("row deleted before the object")
	}
	e.store.fail = false
	e.sweep()
	if _, ok := e.file(loose.ID); ok || !e.store.has(loose.Path) {
		t.Fatalf("unreferenced deleted file: row kept=%v", ok)
	}
}

// Run sweeps at once and returns when its context is cancelled, so the
// orchestrator can stop on SIGTERM.
func TestRunSweepsAtOnceAndStopsWithContext(t *testing.T) {
	e := setup(t)
	src := e.source("a.png")
	e.setExpiry(src.ID, time.Now().Add(-time.Minute))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		e.svc.Run(ctx, time.Hour)
		close(done)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		// A purged file no job points at loses its row too.
		if _, ok := e.file(src.ID); !ok && e.store.has(src.Path) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not sweep the expired file")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
