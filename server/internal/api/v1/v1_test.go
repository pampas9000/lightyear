package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/apikey"
	"transcoder/server/internal/services/jobstate"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/storage"
	"transcoder/server/internal/services/task"
	"transcoder/server/internal/services/upload"
	"transcoder/server/internal/testutil"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type env struct {
	t       *testing.T
	db      *gorm.DB
	app     *fiber.App
	h       *Handler
	keys    *apikey.Service
	owner   uuid.UUID
	deleted *[]string // object keys removed from the (fake) store
}

// recordingStore stands in for the object store when files are deleted.
type recordingStore struct{ deleted *[]string }

func (r recordingStore) DeleteObject(_ context.Context, _, key string) error {
	*r.deleted = append(*r.deleted, key)
	return nil
}

func (recordingStore) AbortMultipartUpload(context.Context, string, string, string) error { return nil }

func (recordingStore) HeadObject(context.Context, string, string) (*s3sdk.HeadObjectOutput, error) {
	return nil, &smithy.GenericAPIError{Code: "NotFound"}
}

func setup(t *testing.T, opts ...func(*Handler, *gorm.DB)) *env {
	t.Helper()
	db := testutil.OpenDatabase(t)
	// Presigning is local, so no store is contacted.
	s3 := storage.NewS3Service(s3sdk.New(s3sdk.Options{
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider("id", "secret", ""),
		BaseEndpoint: aws.String("https://example.r2.cloudflarestorage.com"),
		UsePathStyle: true,
	}))
	sign := func(ctx context.Context, path, filename string, ttl time.Duration) (string, error) {
		return s3.GetPresignedURL(ctx, "bucket", path, filename, false, ttl)
	}
	keys := apikey.NewService(db)
	deleted := &[]string{}
	files := lifecycle.NewService(db, recordingStore{deleted}, "bucket", lifecycle.Days(30, 30))
	h := New(task.NewService(db, nil), upload.NewService(db, s3, "bucket", files), files, sign)
	for _, opt := range opts {
		opt(h, db)
	}
	app := fiber.New()
	h.Install(app, keys)
	return &env{t: t, db: db, app: app, h: h, keys: keys, owner: uuid.New(), deleted: deleted}
}

func (e *env) key(scopes ...string) string {
	e.t.Helper()
	_, plaintext, err := e.keys.Create(e.t.Context(), e.owner, "test", scopes)
	if err != nil {
		e.t.Fatal(err)
	}
	return plaintext
}

type envelope struct {
	Success bool            `json:"success"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Details json.RawMessage `json:"details"`
}

func (e *env) do(method, path, key, body string, headers ...string) (*http.Response, envelope) {
	e.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := e.app.Test(req)
	if err != nil {
		e.t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var env envelope
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &env); err != nil {
			e.t.Fatalf("%s %s: not JSON: %s", method, path, raw)
		}
	}
	return res, env
}

func (e *env) uploaded(name string) models.File {
	e.t.Helper()
	f := models.File{OwnerID: e.owner, Name: name, Path: "uploads/" + e.owner.String() + "/" + uuid.NewString() + ".png", Status: "UPLOADED", Size: 1000}
	if err := e.db.Create(&f).Error; err != nil {
		e.t.Fatal(err)
	}
	return f
}

func taskBody(fileID uuid.UUID, quality int) string {
	return `{"items":[{"file_id":"` + fileID.String() + `"}],"options":{"format":"webp","quality":` + itoa(quality) + `}}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func decode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

func TestAuthenticationAndScopes(t *testing.T) {
	e := setup(t)
	readKey := e.key(apikey.ScopeTasksRead)
	writeKey := e.key(apikey.ScopeTasksWrite)
	file := e.uploaded("a.png")

	for name, key := range map[string]string{"missing": "", "malformed": "nope", "unknown": apikey.KeyPrefix + "AAAAAAAAAAAAAAAAAAAAAAAA"} {
		res, body := e.do(http.MethodGet, "/v1/tasks", key, "")
		if res.StatusCode != http.StatusUnauthorized || body.Code != "error.auth.unauthorized" || res.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("%s key: got %d %s", name, res.StatusCode, body.Code)
		}
	}

	res, body := e.do(http.MethodPost, "/v1/tasks", readKey, taskBody(file.ID, 70))
	if res.StatusCode != http.StatusForbidden || body.Code != "error.auth.insufficient_scope" || !strings.Contains(string(body.Details), "tasks:write") {
		t.Fatalf("read key creating a task: %d %s %s", res.StatusCode, body.Code, body.Details)
	}
	if res, _ := e.do(http.MethodPost, "/v1/uploads", readKey, `{"filename":"a.png","size":10}`); res.StatusCode != http.StatusForbidden {
		t.Fatalf("read key uploading: %d", res.StatusCode)
	}
	if res, body := e.do(http.MethodGet, "/v1/tasks", writeKey, ""); res.StatusCode != http.StatusForbidden || !strings.Contains(string(body.Details), "tasks:read") {
		t.Fatalf("write key listing: %d %s", res.StatusCode, body.Details)
	}
	if res, _ := e.do(http.MethodGet, "/v1/tasks", readKey, ""); res.StatusCode != http.StatusOK {
		t.Fatalf("read key listing: %d", res.StatusCode)
	}
	var count int64
	e.db.Model(&models.Task{}).Count(&count)
	if count != 0 {
		t.Fatalf("a rejected request created %d tasks", count)
	}
}

func TestRevokedKeyIsRejected(t *testing.T) {
	e := setup(t)
	key, plaintext, err := e.keys.Create(t.Context(), e.owner, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := e.do(http.MethodGet, "/v1/tasks", plaintext, ""); res.StatusCode != http.StatusOK {
		t.Fatalf("before revoke: %d", res.StatusCode)
	}
	if _, err := e.keys.Revoke(t.Context(), e.owner, key.ID); err != nil {
		t.Fatal(err)
	}
	if res, body := e.do(http.MethodGet, "/v1/tasks", plaintext, ""); res.StatusCode != http.StatusUnauthorized || body.Code != "error.auth.unauthorized" {
		t.Fatalf("after revoke: %d %s", res.StatusCode, body.Code)
	}
}

func TestCreateTaskReturnsAcceptedWithLocation(t *testing.T) {
	e := setup(t)
	key := e.key()
	file := e.uploaded("a.png")
	res, body := e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 70))
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d: %s %s", res.StatusCode, body.Message, body.Details)
	}
	got := decode[Task](t, body.Data)
	if got.Status != models.TaskPending || len(got.Jobs) != 1 || got.Jobs[0].SourceFile == nil || got.Jobs[0].SourceFile.ID != file.ID {
		t.Fatalf("unexpected task %+v", got)
	}
	if loc := res.Header.Get("Location"); loc != "/v1/tasks/"+got.ID.String() {
		t.Fatalf("Location = %q", loc)
	}
	if res.Header.Get("Retry-After") != "2" || res.Header.Get(HeaderIdempotentReplayed) != "" {
		t.Fatalf("headers: %v", res.Header)
	}
	// The Location resolves with a read key.
	if res, body := e.do(http.MethodGet, res.Header.Get("Location"), key, ""); res.StatusCode != http.StatusOK || decode[Task](t, body.Data).ID != got.ID {
		t.Fatalf("GET Location: %d", res.StatusCode)
	}
	// Strict bodies: /api's idempotency_key field and misspellings are rejected.
	for _, bad := range []string{
		`{"items":[{"file_id":"` + file.ID.String() + `"}],"options":{"format":"webp"},"idempotency_key":"k"}`,
		`{"items":[{"file_id":"` + file.ID.String() + `"}],"option":{"format":"webp"}}`,
		``,
	} {
		if res, body := e.do(http.MethodPost, "/v1/tasks", key, bad); res.StatusCode != http.StatusBadRequest || body.Code != "error.body.invalid" {
			t.Errorf("%q: %d %s", bad, res.StatusCode, body.Code)
		}
	}
	if res, body := e.do(http.MethodGet, "/v1/tasks/"+uuid.NewString(), key, ""); res.StatusCode != http.StatusNotFound || body.Code != "error.task.not_found" {
		t.Fatalf("missing task: %d %s", res.StatusCode, body.Code)
	}
}

func TestIdempotencyKeyHeader(t *testing.T) {
	e := setup(t)
	key := e.key()
	file := e.uploaded("a.png")
	res, body := e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 70), "Idempotency-Key", "batch-1")
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("first: %d %s", res.StatusCode, body.Message)
	}
	first := decode[Task](t, body.Data)
	if first.IdempotencyKey == nil || *first.IdempotencyKey != "batch-1" {
		t.Fatalf("idempotency key not stored: %+v", first.IdempotencyKey)
	}

	// The draft was consumed, but a replay never needs it.
	res, body = e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 70), "Idempotency-Key", "batch-1")
	if res.StatusCode != http.StatusAccepted || res.Header.Get(HeaderIdempotentReplayed) != "true" {
		t.Fatalf("replay: %d %v", res.StatusCode, res.Header)
	}
	if replay := decode[Task](t, body.Data); replay.ID != first.ID || res.Header.Get("Location") != "/v1/tasks/"+first.ID.String() {
		t.Fatalf("replay returned another task: %s", replay.ID)
	}

	res, body = e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 71), "Idempotency-Key", "batch-1")
	if res.StatusCode != http.StatusConflict || body.Code != "error.idempotency.conflict" {
		t.Fatalf("conflict: %d %s", res.StatusCode, body.Code)
	}

	res, body = e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 70), "Idempotency-Key", strings.Repeat("k", 256))
	if res.StatusCode != http.StatusBadRequest || body.Code != "error.param.invalid" {
		t.Fatalf("long key: %d %s", res.StatusCode, body.Code)
	}

	// Without the header every request creates a task.
	var count int64
	e.db.Model(&models.Task{}).Count(&count)
	if count != 1 {
		t.Fatalf("tasks = %d, want 1", count)
	}
}

// complete marks the task's jobs completed with an output file, as the
// orchestrator would.
func (e *env) complete(taskID uuid.UUID) models.File {
	e.t.Helper()
	out := models.File{OwnerID: e.owner, Name: "a.webp", Path: "outputs/" + e.owner.String() + "/" + uuid.NewString() + ".webp", Status: "UPLOADED", Size: 400, MimeType: "image/webp"}
	if err := e.db.Create(&out).Error; err != nil {
		e.t.Fatal(err)
	}
	if err := e.db.Model(&models.Job{}).Where("task_id = ?", taskID).
		Updates(map[string]any{"status": models.JobCompleted, "progress": 100, "output_file_id": out.ID, "next_run_at": nil}).Error; err != nil {
		e.t.Fatal(err)
	}
	if err := jobstate.Aggregate(e.db, taskID); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestGetTaskETag(t *testing.T) {
	e := setup(t)
	key := e.key()
	clock := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	e.h.now = func() time.Time { return clock }
	file := e.uploaded("a.png")
	_, body := e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 70))
	path := "/v1/tasks/" + decode[Task](t, body.Data).ID.String()

	res, _ := e.do(http.MethodGet, path, key, "")
	etag := res.Header.Get("ETag")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(etag, `W/"`) || res.Header.Get("Retry-After") == "" {
		t.Fatalf("GET: %d etag=%q retry-after=%q", res.StatusCode, etag, res.Header.Get("Retry-After"))
	}
	for _, inm := range []string{etag, strings.TrimPrefix(etag, "W/"), `"other", ` + etag, "*"} {
		res, body := e.do(http.MethodGet, path, key, "", "If-None-Match", inm)
		if res.StatusCode != http.StatusNotModified || res.Header.Get("ETag") != etag || body.Code != "" {
			t.Fatalf("If-None-Match %q: %d", inm, res.StatusCode)
		}
	}
	// Without downloadable outputs the tag does not depend on time.
	clock = clock.Add(2 * time.Hour)
	if res, _ := e.do(http.MethodGet, path, key, "", "If-None-Match", etag); res.StatusCode != http.StatusNotModified {
		t.Fatalf("pending task tag changed over time: %d", res.StatusCode)
	}

	// A state change yields a new tag.
	e.complete(decode[Task](t, body.Data).ID)
	res, _ = e.do(http.MethodGet, path, key, "", "If-None-Match", etag)
	completedTag := res.Header.Get("ETag")
	if res.StatusCode != http.StatusOK || completedTag == etag || res.Header.Get("Retry-After") != "" {
		t.Fatalf("after completion: %d %q", res.StatusCode, completedTag)
	}
	// Download URLs expire, so a completed task's tag rolls over every etagWindow.
	clock = clock.Add(etagWindow / 2)
	if res, _ := e.do(http.MethodGet, path, key, "", "If-None-Match", completedTag); res.StatusCode != http.StatusNotModified {
		t.Fatalf("same window: %d", res.StatusCode)
	}
	clock = clock.Add(etagWindow)
	if res, _ := e.do(http.MethodGet, path, key, "", "If-None-Match", completedTag); res.StatusCode != http.StatusOK {
		t.Fatalf("next window should refresh the URLs, got %d", res.StatusCode)
	}
}

func TestCompletedJobCarriesDownloadURL(t *testing.T) {
	e := setup(t)
	key := e.key(apikey.ScopeTasksRead, apikey.ScopeTasksWrite)
	clock := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	e.h.now = func() time.Time { return clock }
	file := e.uploaded("photo.png")
	_, body := e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 70))
	created := decode[Task](t, body.Data)
	if created.Jobs[0].OutputFile != nil {
		t.Fatal("pending job has an output")
	}
	out := e.complete(created.ID)

	res, body := e.do(http.MethodGet, "/v1/tasks/"+created.ID.String(), key, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET: %d", res.StatusCode)
	}
	got := decode[Task](t, body.Data)
	job := got.Jobs[0]
	if got.Status != models.TaskCompleted || job.OutputFile == nil || job.OutputFile.ID != out.ID {
		t.Fatalf("unexpected task %+v", got)
	}
	if job.ResolvedOptions == nil || job.Compression == nil || job.Compression.OutputBytes != 400 || job.Compression.SourceBytes != 1000 {
		t.Fatalf("resolved_options/compression missing: %+v %+v", job.ResolvedOptions, job.Compression)
	}
	u, err := url.Parse(job.OutputFile.DownloadURL)
	if err != nil || !strings.HasSuffix(u.Path, out.Path) {
		t.Fatalf("download_url %q", job.OutputFile.DownloadURL)
	}
	q := u.Query()
	if q.Get("X-Amz-Expires") != "3600" || !strings.Contains(q.Get("response-content-disposition"), `filename=a.webp`) {
		t.Fatalf("download_url query %v", q)
	}
	if exp := job.OutputFile.DownloadExpiresAt; exp == nil || !exp.Equal(clock.Add(DownloadURLTTL)) {
		t.Fatalf("download_expires_at = %v", exp)
	}
	// Lists stay cheap: no URLs there.
	_, body = e.do(http.MethodGet, "/v1/tasks", key, "")
	list := decode[TaskList](t, body.Data)
	if list.Total != 1 || list.Tasks[0].Jobs[0].OutputFile == nil || list.Tasks[0].Jobs[0].OutputFile.DownloadURL != "" {
		t.Fatalf("list: %+v", list)
	}
}

func TestCancelAndRetry(t *testing.T) {
	e := setup(t)
	key := e.key()
	file := e.uploaded("a.png")
	_, body := e.do(http.MethodPost, "/v1/tasks", key, taskBody(file.ID, 70))
	id := decode[Task](t, body.Data).ID.String()

	res, body := e.do(http.MethodPost, "/v1/tasks/"+id+"/cancel", key, "")
	if res.StatusCode != http.StatusOK || decode[Task](t, body.Data).Status != models.TaskCancelled {
		t.Fatalf("cancel: %d %s", res.StatusCode, body.Data)
	}
	res, body = e.do(http.MethodPost, "/v1/tasks/"+id+"/retry", key, `{"retry_all":true}`)
	if res.StatusCode != http.StatusAccepted || res.Header.Get("Location") != "/v1/tasks/"+id || decode[Task](t, body.Data).Status != models.TaskPending {
		t.Fatalf("retry: %d %s", res.StatusCode, body.Data)
	}
	jobID := decode[Task](t, body.Data).Jobs[0].ID.String()
	res, body = e.do(http.MethodPost, "/v1/tasks/"+id+"/retry", key, `{"job_ids":["`+jobID+`"]}`)
	if res.StatusCode != http.StatusConflict || body.Code != "error.task.conflict" {
		t.Fatalf("retrying an active job: %d %s", res.StatusCode, body.Code)
	}
	if res, body := e.do(http.MethodGet, "/v1/tasks?limit=101", key, ""); res.StatusCode != http.StatusBadRequest || body.Code != "error.param.invalid" {
		t.Fatalf("limit=101: %d", res.StatusCode)
	}
	if res, _ := e.do(http.MethodGet, "/v1/tasks?status=pending", key, ""); res.StatusCode != http.StatusOK {
		t.Fatalf("status filter: %d", res.StatusCode)
	}
}

func TestUploads(t *testing.T) {
	e := setup(t)
	key := e.key()
	res, body := e.do(http.MethodPost, "/v1/uploads", key, `{"filename":"a.png","content_type":"image/png","size":1024,"sha256":"`+strings.Repeat("ab", 32)+`"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create upload: %d %s %s", res.StatusCode, body.Message, body.Details)
	}
	up := decode[Upload](t, body.Data)
	if up.Method != "put" || up.URL == "" || up.ExpiresAt == nil || up.File.Status != "UPLOADING" || up.File.SHA256 != strings.Repeat("ab", 32) {
		t.Fatalf("upload: %+v", up)
	}
	if !strings.Contains(up.URL, "X-Amz-Checksum-Sha256") {
		t.Fatalf("checksum not signed into %s", up.URL)
	}
	// A file that is not uploaded cannot be submitted.
	if res, body := e.do(http.MethodPost, "/v1/tasks", key, taskBody(up.File.ID, 70)); res.StatusCode != http.StatusBadRequest || body.Code != "error.param.invalid" {
		t.Fatalf("task on uploading file: %d %s", res.StatusCode, body.Code)
	}
	// Once uploaded, the same content is not uploaded again.
	if err := e.db.Model(&models.File{}).Where("id = ?", up.File.ID).Update("status", "UPLOADED").Error; err != nil {
		t.Fatal(err)
	}
	res, body = e.do(http.MethodPost, "/v1/uploads", key, `{"filename":"b.png","size":1024,"sha256":"`+strings.Repeat("AB", 32)+`"}`)
	if fast := decode[Upload](t, body.Data); res.StatusCode != http.StatusOK || fast.Method != "none" || fast.File.ID != up.File.ID || fast.URL != "" {
		t.Fatalf("fast upload: %d %+v", res.StatusCode, fast)
	}
	if res, body := e.do(http.MethodPost, "/v1/uploads/"+up.File.ID.String()+"/complete", key, ""); res.StatusCode != http.StatusOK || decode[File](t, body.Data).ID != up.File.ID {
		t.Fatalf("completing an uploaded file: %d %s", res.StatusCode, body.Message)
	}
	for _, bad := range []string{`{"filename":"a.png","size":0}`, `{"filename":"a.png","size":1,"sha256":"xyz"}`, `{"size":1}`, `{"filename":"a.png","size":1,"type":"image/png"}`} {
		if res, _ := e.do(http.MethodPost, "/v1/uploads", key, bad); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, res.StatusCode)
		}
	}
	// Another owner's file is invisible.
	other := models.File{OwnerID: uuid.New(), Name: "x.png", Path: "uploads/x/y.png", Status: "UPLOADING"}
	e.db.Create(&other)
	if res, body := e.do(http.MethodPost, "/v1/uploads/"+other.ID.String()+"/complete", key, ""); res.StatusCode != http.StatusNotFound || body.Code != "error.file.not_found" {
		t.Fatalf("foreign upload: %d %s", res.StatusCode, body.Code)
	}
	if res, _ := e.do(http.MethodPost, "/v1/uploads/not-a-uuid/complete", key, ""); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad upload id: %d", res.StatusCode)
	}
	if res, _ := e.do(http.MethodPost, "/v1/uploads/"+up.File.ID.String()+"/parts/1/url", key, ""); res.StatusCode != http.StatusConflict {
		t.Fatalf("signing a part of a single-PUT upload: %d", res.StatusCode)
	}
}

// Aborting a multipart upload deletes it; a deleted upload cannot be
// resumed, signed or completed back into existence.
func TestAbortedUploadStaysDeleted(t *testing.T) {
	e := setup(t)
	key := e.key()
	expires := time.Now().Add(24 * time.Hour)
	up := models.File{OwnerID: e.owner, Name: "big.png", Path: "uploads/" + e.owner.String() + "/" + uuid.NewString() + ".png",
		Status: models.FileUploading, UploadID: "mp-1", UploadExpiresAt: &expires, Size: 20 << 20}
	if err := e.db.Create(&up).Error; err != nil {
		t.Fatal(err)
	}
	res, body := e.do(http.MethodPost, "/v1/uploads/"+up.ID.String()+"/abort", key, "")
	if res.StatusCode != http.StatusOK || decode[File](t, body.Data).Status != models.FileDeleted {
		t.Fatalf("abort: %d %s %s", res.StatusCode, body.Message, body.Data)
	}
	for _, path := range []string{"/complete", "/parts/1/url"} {
		if res, body := e.do(http.MethodPost, "/v1/uploads/"+up.ID.String()+path, key, `{"parts":[{"part_number":1,"etag":"x"}]}`); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s after abort: %d %s", path, res.StatusCode, body.Message)
		}
	}
	if res, _ := e.do(http.MethodGet, "/v1/uploads/"+up.ID.String()+"/parts", key, ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("list parts after abort: %d", res.StatusCode)
	}
	if res, _ := e.do(http.MethodPost, "/v1/uploads/"+up.ID.String()+"/abort", key, ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("second abort: %d", res.StatusCode)
	}
	var f models.File
	if err := e.db.First(&f, "id = ?", up.ID).Error; err != nil || f.Status != models.FileDeleted {
		t.Fatalf("file after abort: %+v %v", f, err)
	}
}
