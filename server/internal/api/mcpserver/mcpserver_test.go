package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"transcoder/server/internal/api/response"
	v1 "transcoder/server/internal/api/v1"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/apikey"
	"transcoder/server/internal/services/jobstate"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/storage"
	"transcoder/server/internal/services/task"
	"transcoder/server/internal/services/upload"
	"transcoder/server/internal/services/usage"
	"transcoder/server/internal/testutil"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"
)

// --- Without a database: the tool catalogue and its schemas. ---

func newBare(t *testing.T) *Server {
	t.Helper()
	s, err := New(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// connect opens a client session to h, sending key (if any) as a bearer token.
func connect(t *testing.T, h http.Handler, key string) *sdk.ClientSession {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(t.Context(), &sdk.StreamableClientTransport{
		Endpoint:   srv.URL,
		HTTPClient: &http.Client{Transport: bearer{key}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type bearer struct{ key string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.key != "" {
		r.Header.Set("Authorization", "Bearer "+b.key)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// schemaOf resolves a tool schema as a client would see it on the wire.
func schemaOf(t *testing.T, v any) *jsonschema.Resolved {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	r, err := s.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve %s: %v", b, err)
	}
	return r
}

func validates(t *testing.T, r *jsonschema.Resolved, instance string) error {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(instance), &v); err != nil {
		t.Fatal(err)
	}
	return r.Validate(v)
}

func TestToolSchemas(t *testing.T) {
	s := newBare(t)
	// Without authentication, so no database is needed.
	session := connect(t, s.sdkHandler(), "")
	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*sdk.Tool{}
	for _, tool := range res.Tools {
		tools[tool.Name] = tool
	}
	for _, name := range []string{"create_upload", "submit_task", "get_task"} {
		if tools[name] == nil || tools[name].OutputSchema == nil || tools[name].Description == "" {
			t.Fatalf("tool %s missing or incomplete: %+v", name, tools[name])
		}
	}
	if !tools["get_task"].Annotations.ReadOnlyHint {
		t.Error("get_task must be read-only")
	}

	submit := schemaOf(t, tools["submit_task"].InputSchema)
	for _, ok := range []string{
		`{"items":[{"url":"https://example.com/a.jpg"}],"options":{"format":"avif"}}`,
		`{"items":[{"file_id":"0198a2f0-0000-7000-8000-000000000000"}],"options":{"format":"webp","quality":80},"target":{"max_bytes":102400}}`,
		`{"items":[{"url":"https://example.com/a.jpg"}],"outputs":[{"name":"small","options":{"format":"avif"},"target":{"max_bytes":5000}},{"options":{"format":"jpeg"}}],"idempotency_key":"k1"}`,
	} {
		if err := validates(t, submit, ok); err != nil {
			t.Errorf("valid arguments rejected: %s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		`{"options":{"format":"avif"}}`,                                                        // no items
		`{"items":[{"url":"https://example.com/a.jpg"}],"options":{"quality":80}}`,             // ImageOptions needs format
		`{"items":[{"url":"https://example.com/a.jpg"}],"options":{"format":"bmp"}}`,           // unknown format
		`{"items":[{"url":"https://example.com/a.jpg"}],"options":{"format":"avif","x":1}}`,    // unknown option
		`{"items":[{"url":"u","file_id":"0198a2f0-0000-7000-8000-000000000000"}]}`,             // two sources in one item
		`{"items":[{"url":"https://example.com/a.jpg"}],"target":{"max_bytes":0}}`,             // max_bytes must be positive
		`{"items":[{"url":"https://example.com/a.jpg"}],"options":{"format":"avif"},"y":true}`, // unknown argument
	} {
		if err := validates(t, submit, bad); err == nil {
			t.Errorf("invalid arguments accepted: %s", bad)
		}
	}

	// Input schemas use one type per schema, for clients that map them onto
	// single-type dialects; no schema uses a format validators do not know.
	for name, tool := range tools {
		for kind, schema := range map[string]any{"input": tool.InputSchema, "output": tool.OutputSchema} {
			b, _ := json.Marshal(schema)
			var walk func(path string, v any)
			walk = func(path string, v any) {
				switch v := v.(type) {
				case map[string]any:
					if _, ok := v["type"].([]any); ok && kind == "input" {
						t.Errorf("%s input schema: type array at %s", name, path)
					}
					if f, ok := v["format"].(string); ok && !slices.Contains([]string{"uuid", "date-time", "uri"}, f) {
						t.Errorf("%s %s schema: format %q at %s", name, kind, f, path)
					}
					for k, sub := range v {
						walk(path+"/"+k, sub)
					}
				case []any:
					for i, sub := range v {
						walk(fmt.Sprintf("%s/%d", path, i), sub)
					}
				}
			}
			var v any
			_ = json.Unmarshal(b, &v)
			walk("", v)
		}
	}

	getTask := schemaOf(t, tools["get_task"].InputSchema)
	if err := validates(t, getTask, `{"task_id":"0198a2f0-0000-7000-8000-000000000000","wait_seconds":31}`); err == nil {
		t.Error("wait_seconds above the cap accepted")
	}
	// The output schema describes /v1 tasks, options included.
	out := schemaOf(t, tools["get_task"].OutputSchema)
	task := `{"id":"0198a2f0-0000-7000-8000-000000000000","status":"COMPLETED","jobs":[{"id":"0198a2f0-0000-7000-8000-000000000001",
		"status":"COMPLETED","progress":100,"output_index":0,"options":{"format":"avif"},"resolved_options":{"format":"avif","quality":60},
		"attempts":1,"max_attempts":3,"created_at":"2026-11-18T00:00:00Z","updated_at":"2026-11-18T00:00:00Z"}],
		"created_at":"2026-11-18T00:00:00Z","updated_at":"2026-11-18T00:00:00Z"}`
	if err := validates(t, out, task); err != nil {
		t.Errorf("task rejected by output schema: %v", err)
	}
	if err := validates(t, out, strings.Replace(task, `"format":"avif"}`, `"format":"tiff"}`, 1)); err == nil {
		t.Error("output schema accepts an unknown format")
	}
}

// sdkHandler is the MCP handler without authentication, for tests that do
// not call tools.
func (s *Server) sdkHandler() http.Handler {
	return sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s.mcp },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}

// --- With a database. ---

type env struct {
	t      *testing.T
	db     *gorm.DB
	s      *Server
	keys   *apikey.Service
	owner  uuid.UUID
	store  *fakeStore
	tasks  *task.Service
	upload *upload.Service
}

func setup(t *testing.T, opts ...func(*env)) *env {
	t.Helper()
	db := testutil.OpenDatabase(t)
	store := newFakeStore(t)
	s3 := storage.NewS3Service(s3sdk.New(s3sdk.Options{
		Region:           "auto",
		Credentials:      credentials.NewStaticCredentialsProvider("id", "secret", ""),
		BaseEndpoint:     aws.String(store.srv.URL),
		UsePathStyle:     true,
		RetryMaxAttempts: 1,
	}))
	sign := func(ctx context.Context, path, filename string, ttl time.Duration) (string, error) {
		return s3.GetPresignedURL(ctx, "bucket", path, filename, false, ttl)
	}
	e := &env{
		t: t, db: db, keys: apikey.NewService(db), owner: uuid.New(), store: store,
		tasks:  task.NewService(db, nil),
		upload: upload.NewService(db, s3, "bucket", lifecycle.Days(30, 30)),
	}
	var err error
	e.s, err = New(e.tasks, e.upload, e.keys, sign, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.s.poll = 20 * time.Millisecond
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func (e *env) key(scopes ...string) string {
	e.t.Helper()
	_, plaintext, err := e.keys.Create(e.t.Context(), e.owner, "test", scopes)
	if err != nil {
		e.t.Fatal(err)
	}
	return plaintext
}

func (e *env) session(key string) *sdk.ClientSession {
	return connect(e.t, e.s.Handler(), key)
}

func (e *env) uploaded(name string) models.File {
	e.t.Helper()
	f := models.File{OwnerID: e.owner, Name: name, Path: "uploads/" + e.owner.String() + "/" + uuid.NewString() + ".png", Status: "UPLOADED", Size: 1000}
	if err := e.db.Create(&f).Error; err != nil {
		e.t.Fatal(err)
	}
	return f
}

// call invokes a tool and returns its structured result, or the tool error.
func call[T any](t *testing.T, session *sdk.ClientSession, name string, args any) (T, *toolError) {
	t.Helper()
	var zero T
	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if res.IsError {
		var te toolError
		if err := json.Unmarshal([]byte(text), &te); err != nil {
			t.Fatalf("%s: error is not JSON: %s", name, text)
		}
		if res.StructuredContent != nil {
			t.Errorf("%s: an error result must not carry structuredContent", name)
		}
		return zero, &te
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !json.Valid([]byte(text)) || string(b) != compact(t, text) {
		t.Errorf("%s: text content is not the structured result: %s", name, text)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s: decode %s: %v", name, b, err)
	}
	return out, nil
}

func compact(t *testing.T, s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func detail(t *testing.T, e *toolError) map[string]any {
	t.Helper()
	b, _ := json.Marshal(e.Details)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func TestAuthentication(t *testing.T) {
	e := setup(t)
	for name, key := range map[string]string{"missing": "", "unknown": apikey.KeyPrefix + "AAAAAAAAAAAAAAAAAAAAAAAA"} {
		srv := httptest.NewServer(e.s.Handler())
		req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		srv.Close()
		if res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") != `Bearer realm="lightyear"` {
			t.Errorf("%s key: %d %q %s", name, res.StatusCode, res.Header.Get("WWW-Authenticate"), body)
		}
	}

	key, plaintext, err := e.keys.Create(t.Context(), e.owner, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	session := e.session(plaintext)
	if _, err := session.ListTools(t.Context(), nil); err != nil {
		t.Fatalf("valid key: %v", err)
	}
	if _, err := e.keys.Revoke(t.Context(), e.owner, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.ListTools(t.Context(), nil); err == nil {
		t.Fatal("revoked key still accepted")
	}
}

func TestScopes(t *testing.T) {
	e := setup(t)
	file := e.uploaded("a.png")
	read := e.session(e.key(apikey.ScopeTasksRead))
	write := e.session(e.key(apikey.ScopeTasksWrite))

	_, terr := call[v1.Task](t, read, "submit_task", map[string]any{"items": []any{map[string]any{"file_id": file.ID}}, "options": map[string]any{"format": "webp"}})
	if terr == nil || terr.Code != "error.auth.insufficient_scope" || detail(t, terr)["required_scope"] != "tasks:write" {
		t.Fatalf("read key submitting: %+v", terr)
	}
	if _, terr := call[v1.Upload](t, read, "create_upload", map[string]any{"filename": "a.png", "size": 10}); terr == nil || terr.Code != "error.auth.insufficient_scope" {
		t.Fatalf("read key uploading: %+v", terr)
	}
	created, terr := call[v1.Task](t, write, "submit_task", map[string]any{"items": []any{map[string]any{"file_id": file.ID}}, "options": map[string]any{"format": "webp"}})
	if terr != nil {
		t.Fatal(terr)
	}
	if _, terr := call[v1.Task](t, write, "get_task", map[string]any{"task_id": created.ID}); terr == nil || detail(t, terr)["required_scope"] != "tasks:read" {
		t.Fatalf("write key reading: %+v", terr)
	}
	if got, terr := call[v1.Task](t, read, "get_task", map[string]any{"task_id": created.ID}); terr != nil || got.ID != created.ID {
		t.Fatalf("read key reading: %+v %+v", got, terr)
	}
}

func TestSubmitTask(t *testing.T) {
	e := setup(t)
	session := e.session(e.key())
	file := e.uploaded("a.png")

	got, terr := call[v1.Task](t, session, "submit_task", map[string]any{
		"items": []any{map[string]any{"file_id": file.ID}, map[string]any{"url": "https://images.example.com/cat.jpg"}},
		"outputs": []any{
			map[string]any{"name": "small", "options": map[string]any{"format": "avif"}, "target": map[string]any{"max_bytes": 20000}},
			map[string]any{"options": map[string]any{"format": "jpeg", "quality": 80}},
		},
		"idempotency_key": "batch-1",
	})
	if terr != nil {
		t.Fatal(terr)
	}
	if got.Status != models.TaskPending || len(got.Jobs) != 4 {
		t.Fatalf("task: %s with %d jobs", got.Status, len(got.Jobs))
	}
	var targets int
	for _, j := range got.Jobs {
		if j.Target != nil && j.Target.MaxBytes != nil && *j.Target.MaxBytes == 20000 {
			targets++
		}
	}
	if targets != 2 {
		t.Fatalf("want the target on 2 jobs, got %d", targets)
	}

	// The same key and request replays the task; a different request conflicts.
	again, terr := call[v1.Task](t, session, "submit_task", map[string]any{
		"items": []any{map[string]any{"url": "https://images.example.com/cat.jpg"}, map[string]any{"file_id": file.ID}},
		"outputs": []any{
			map[string]any{"name": "small", "options": map[string]any{"format": "avif"}, "target": map[string]any{"max_bytes": 20000}},
			map[string]any{"options": map[string]any{"format": "jpeg", "quality": 80}},
		},
		"idempotency_key": "batch-1",
	})
	if terr != nil || again.ID != got.ID {
		t.Fatalf("replay: %+v %+v", again.ID, terr)
	}
	_, terr = call[v1.Task](t, session, "submit_task", map[string]any{
		"items": []any{map[string]any{"file_id": file.ID}}, "options": map[string]any{"format": "png"}, "idempotency_key": "batch-1",
	})
	if terr == nil || terr.Code != "error.idempotency.conflict" {
		t.Fatalf("conflict: %+v", terr)
	}

	// Errors of the task service keep their /v1 codes.
	for name, c := range map[string]struct {
		args map[string]any
		code response.ResponseCode
	}{
		"schema":         {map[string]any{"items": []any{map[string]any{"file_id": file.ID}}, "options": map[string]any{"quality": 80}}, "error.body.invalid"},
		"no options":     {map[string]any{"items": []any{map[string]any{"file_id": file.ID}}}, "error.param.invalid"},
		"png target":     {map[string]any{"items": []any{map[string]any{"file_id": file.ID}}, "options": map[string]any{"format": "png"}, "target": map[string]any{"max_bytes": 100}}, "error.param.invalid"},
		"unknown file":   {map[string]any{"items": []any{map[string]any{"file_id": uuid.New()}}, "options": map[string]any{"format": "png"}}, "error.param.invalid"},
		"bad url":        {map[string]any{"items": []any{map[string]any{"url": "ftp://example.com/a.jpg"}}, "options": map[string]any{"format": "png"}}, "error.param.invalid"},
		"bad options":    {map[string]any{"items": []any{map[string]any{"file_id": file.ID}}, "options": map[string]any{"format": "jpeg", "quality": 101}}, "error.body.invalid"},
		"semantic error": {map[string]any{"items": []any{map[string]any{"file_id": file.ID}}, "options": map[string]any{"format": "jpeg", "lossless": true}}, "error.param.invalid"},
	} {
		if _, terr := call[v1.Task](t, session, "submit_task", c.args); terr == nil || terr.Code != c.code {
			t.Errorf("%s: want %s, got %+v", name, c.code, terr)
		}
	}
}

// complete marks every job of the task as done with a stored output.
func (e *env) complete(taskID uuid.UUID) {
	e.t.Helper()
	out := models.File{OwnerID: e.owner, Name: "a.webp", Path: "outputs/" + e.owner.String() + "/" + uuid.NewString() + ".webp", Status: "UPLOADED", Size: 400, MimeType: "image/webp"}
	if err := e.db.Create(&out).Error; err != nil {
		e.t.Error(err)
		return
	}
	if err := e.db.Model(&models.Job{}).Where("task_id = ?", taskID).
		Updates(map[string]any{"status": models.JobCompleted, "progress": 100, "output_file_id": out.ID, "next_run_at": nil}).Error; err != nil {
		e.t.Error(err)
		return
	}
	if err := jobstate.Aggregate(e.db, taskID); err != nil {
		e.t.Error(err)
	}
}

func TestGetTaskWaits(t *testing.T) {
	e := setup(t)
	session := e.session(e.key())
	file := e.uploaded("a.png")
	created, terr := call[v1.Task](t, session, "submit_task", map[string]any{"items": []any{map[string]any{"file_id": file.ID}}, "options": map[string]any{"format": "webp"}})
	if terr != nil {
		t.Fatal(terr)
	}

	// wait_seconds 0 answers at once.
	start := time.Now()
	got, terr := call[v1.Task](t, session, "get_task", map[string]any{"task_id": created.ID})
	if terr != nil || got.Status != models.TaskPending || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("no wait: %s %+v after %v", got.Status, terr, time.Since(start))
	}

	// A wait ends as soon as the task finishes, with download links.
	var wg sync.WaitGroup
	wg.Go(func() {
		time.Sleep(150 * time.Millisecond)
		e.complete(created.ID)
	})
	start = time.Now()
	got, terr = call[v1.Task](t, session, "get_task", map[string]any{"task_id": created.ID, "wait_seconds": 10})
	wg.Wait()
	if terr != nil || got.Status != models.TaskCompleted {
		t.Fatalf("wait: %s %+v", got.Status, terr)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("wait did not end when the task finished: %v", elapsed)
	}
	out := got.Jobs[0].OutputFile
	if out == nil || !strings.Contains(out.DownloadURL, "X-Amz-Signature") || out.DownloadExpiresAt == nil || got.Jobs[0].Compression == nil {
		t.Fatalf("completed job without download link or compression: %+v", got.Jobs[0])
	}

	// An unfinished task is answered when the wait runs out.
	other, _ := call[v1.Task](t, session, "submit_task", map[string]any{"items": []any{map[string]any{"file_id": file.ID}}, "options": map[string]any{"format": "avif"}})
	start = time.Now()
	got, terr = call[v1.Task](t, session, "get_task", map[string]any{"task_id": other.ID, "wait_seconds": 1})
	if elapsed := time.Since(start); terr != nil || got.Status != models.TaskPending || elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("timed-out wait: %s %+v after %v", got.Status, terr, elapsed)
	}

	// Someone else's task does not exist.
	stranger := setupFor(e, uuid.New())
	if _, terr := call[v1.Task](t, stranger.session(stranger.key()), "get_task", map[string]any{"task_id": created.ID}); terr == nil || terr.Code != "error.task.not_found" {
		t.Fatalf("other owner's task: %+v", terr)
	}
}

// setupFor is e with another owner.
func setupFor(e *env, owner uuid.UUID) *env {
	c := *e
	c.owner = owner
	return &c
}

func TestQuotaAndRateLimitErrors(t *testing.T) {
	limiter := &fakeLimiter{n: 2}
	e := setup(t, func(e *env) {
		q := usage.NewQuotas(e.db, usage.Limits{MonthlyUnits: 1, StorageBytes: 1500})
		e.tasks.WithQuotas(q)
		e.upload.WithQuotas(q)
		e.s.limiter = limiter
	})
	session := e.session(e.key())
	file := e.uploaded("a.png")

	if _, terr := call[v1.Task](t, session, "submit_task", map[string]any{"items": []any{map[string]any{"file_id": file.ID}}, "options": map[string]any{"format": "webp"}}); terr != nil {
		t.Fatal(terr)
	}
	_, terr := call[v1.Task](t, session, "submit_task", map[string]any{"items": []any{map[string]any{"file_id": file.ID}}, "options": map[string]any{"format": "avif"}})
	d := detail(t, terr)
	if terr == nil || terr.Code != "error.quota.exceeded" || d["quota"] != "monthly_units" || d["limit"] != 1.0 || d["reserved"] != 1.0 || d["requested"] != 1.0 {
		t.Fatalf("quota: %+v", terr)
	}

	// The bucket is the user's /v1 write bucket: the two calls above used it up.
	_, terr = call[v1.Upload](t, session, "create_upload", map[string]any{"filename": "b.png", "size": 10})
	if terr == nil || terr.Code != "error.rate_limit.exceeded" || detail(t, terr)["retry_after"] != 2.0 {
		t.Fatalf("rate limit: %+v", terr)
	}
	if limiter.calls[e.owner.String()] != 3 {
		t.Fatalf("limiter keyed by user: %v", limiter.calls)
	}
	// Reads are not limited.
	if _, terr := call[v1.Task](t, session, "get_task", map[string]any{"task_id": uuid.New()}); terr == nil || terr.Code != "error.task.not_found" {
		t.Fatalf("read after the limit: %+v", terr)
	}
}

type fakeLimiter struct {
	mu    sync.Mutex
	n     int
	calls map[string]int
}

func (l *fakeLimiter) Allow(_ context.Context, key string) (bool, time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.calls == nil {
		l.calls = map[string]int{}
	}
	l.calls[key]++
	return l.calls[key] <= l.n, 1500 * time.Millisecond, nil
}

// --- Uploads: create_upload signs, submit_task finishes. ---

// fakeStore answers the S3 calls an upload makes: create and list a
// multipart upload, complete it, and HEAD the object.
type fakeStore struct {
	srv *httptest.Server

	mu      sync.Mutex
	objects map[string]int64           // key -> size
	parts   map[string]map[int32]int64 // key -> part number -> size
}

func newFakeStore(t *testing.T) *fakeStore {
	f := &fakeStore{objects: map[string]int64{}, parts: map[string]map[int32]int64{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeStore) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/bucket/")
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		fmt.Fprintf(w, `<InitiateMultipartUploadResult><Bucket>bucket</Bucket><Key>%s</Key><UploadId>up-1</UploadId></InitiateMultipartUploadResult>`, key)
	case r.Method == http.MethodGet && q.Has("uploadId"):
		fmt.Fprint(w, `<ListPartsResult><IsTruncated>false</IsTruncated>`)
		for n, size := range f.parts[key] {
			fmt.Fprintf(w, `<Part><PartNumber>%d</PartNumber><ETag>"etag-%d"</ETag><Size>%d</Size></Part>`, n, n, size)
		}
		fmt.Fprint(w, `</ListPartsResult>`)
	case r.Method == http.MethodPost && q.Has("uploadId"):
		var total int64
		for _, size := range f.parts[key] {
			total += size
		}
		f.objects[key] = total
		fmt.Fprintf(w, `<CompleteMultipartUploadResult><Key>%s</Key><ETag>"done"</ETag></CompleteMultipartUploadResult>`, key)
	case r.Method == http.MethodHead:
		size, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(size))
		w.Header().Set("Content-Type", "image/png")
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.String(), http.StatusBadRequest)
	}
}

func (f *fakeStore) put(path string, size int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[path] = size
}

func (f *fakeStore) putPart(path string, n int32, size int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.parts[path] == nil {
		f.parts[path] = map[int32]int64{}
	}
	f.parts[path][n] = size
}

func (e *env) path(id uuid.UUID) string {
	var f models.File
	if err := e.db.First(&f, "id = ?", id).Error; err != nil {
		e.t.Fatal(err)
	}
	return f.Path
}

func TestUploadThenSubmit(t *testing.T) {
	e := setup(t)
	session := e.session(e.key())
	submit := func(id uuid.UUID) (v1.Task, *toolError) {
		return call[v1.Task](t, session, "submit_task", map[string]any{"items": []any{map[string]any{"file_id": id}}, "options": map[string]any{"format": "webp"}})
	}

	up, terr := call[v1.Upload](t, session, "create_upload", map[string]any{"filename": "a.png", "size": 1234, "content_type": "image/png"})
	if terr != nil || up.Method != "put" || !strings.Contains(up.URL, "X-Amz-Signature") || up.File.Status != "UPLOADING" {
		t.Fatalf("create_upload: %+v %+v", up, terr)
	}
	// Before the bytes arrive, submitting says so.
	if _, terr := submit(up.File.ID); terr == nil || terr.Code != "error.param.invalid" || !strings.Contains(terr.Message, "has not been uploaded yet") {
		t.Fatalf("submit before PUT: %+v", terr)
	}
	e.store.put(e.path(up.File.ID), 1234)
	got, terr := submit(up.File.ID)
	if terr != nil || len(got.Jobs) != 1 || got.Jobs[0].SourceFile.Status != "UPLOADED" || got.Jobs[0].SourceFile.Size != 1234 {
		t.Fatalf("submit after PUT: %+v %+v", got, terr)
	}

	// Multipart: finished only when all parts add up to the declared size.
	const size = 11 << 20
	up, terr = call[v1.Upload](t, session, "create_upload", map[string]any{"filename": "big.png", "size": size})
	if terr != nil || up.Method != "multipart" || len(up.PartURLs) != 3 || up.PartSize != 5<<20 {
		t.Fatalf("create_upload multipart: %+v %+v", up, terr)
	}
	path := e.path(up.File.ID)
	e.store.putPart(path, 1, 5<<20)
	e.store.putPart(path, 3, 1<<20)
	if _, terr := submit(up.File.ID); terr == nil || !strings.Contains(terr.Message, "part 2 is missing") {
		t.Fatalf("submit with a missing part: %+v", terr)
	}
	e.store.putPart(path, 2, 4<<20)
	if _, terr := submit(up.File.ID); terr == nil || !strings.Contains(terr.Message, "10485760 of 11534336 bytes") {
		t.Fatalf("submit with a short part: %+v", terr)
	}
	e.store.putPart(path, 2, 5<<20)
	if got, terr := submit(up.File.ID); terr != nil || got.Jobs[0].SourceFile.Size != size {
		t.Fatalf("submit after all parts: %+v %+v", got, terr)
	}

	// The same bytes again (by sha256) need no upload.
	sum := strings.Repeat("ab", 32)
	if err := e.db.Model(&models.File{}).Where("id = ?", up.File.ID).Update("hash", sum).Error; err != nil {
		t.Fatal(err)
	}
	again, terr := call[v1.Upload](t, session, "create_upload", map[string]any{"filename": "big.png", "size": size, "sha256": sum})
	if terr != nil || again.Method != "none" || again.File.ID != up.File.ID || again.URL != "" {
		t.Fatalf("fast upload: %+v %+v", again, terr)
	}
}

// The endpoint is mounted on the Fiber app through the net/http adaptor.
func TestInstallOnFiber(t *testing.T) {
	e := setup(t)
	app := fiber.New()
	e.s.Install(app)
	req := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+e.key())
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("POST %s: %d %s %s", Path, res.StatusCode, res.Header.Get("Content-Type"), body)
	}
	var rpc struct {
		Result struct{ Tools []struct{ Name string } }
	}
	if err := json.Unmarshal(body, &rpc); err != nil {
		t.Fatalf("not JSON-RPC: %s", body)
	}
	var names []string
	for _, tool := range rpc.Result.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"create_upload", "get_task", "submit_task"}) {
		t.Fatalf("tools over Fiber: %v", names)
	}
	// Every method needs a key, GET included.
	res, err = app.Test(httptest.NewRequest(http.MethodGet, Path, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET without key: %d", res.StatusCode)
	}
	// With a key, GET (a server-sent event stream) does not exist: no sessions.
	get := httptest.NewRequest(http.MethodGet, Path, nil)
	get.Header.Set("Accept", "text/event-stream")
	get.Header.Set("Authorization", "Bearer "+e.key())
	get.Header.Set("MCP-Protocol-Version", "2025-11-25")
	if res, err = app.Test(get); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET with key: %d", res.StatusCode)
	}
}

// An internal error keeps the cause in the log and reports the request ID
// that the response carries, as /v1 does.
func TestInternalErrorReportsRequestID(t *testing.T) {
	e := setup(t)
	key := e.key()
	if err := e.db.Exec("DROP TABLE tasks CASCADE").Error; err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Use(requestid.New())
	e.s.Install(app)
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_task","arguments":{"task_id":%q}}}`, uuid.New())
	req := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	var rpc struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct{ Text string }
		}
	}
	if err := json.Unmarshal(raw, &rpc); err != nil || len(rpc.Result.Content) != 1 {
		t.Fatalf("not a tool result: %s", raw)
	}
	var te struct {
		Code    string
		Message string
		Details map[string]string
	}
	if err := json.Unmarshal([]byte(rpc.Result.Content[0].Text), &te); err != nil {
		t.Fatal(err)
	}
	rid := res.Header.Get(fiber.HeaderXRequestID)
	if !rpc.Result.IsError || te.Code != "error.internal" || rid == "" || te.Details["request_id"] != rid {
		t.Fatalf("internal error: %+v, X-Request-ID %q", te, rid)
	}
	if strings.Contains(string(raw), "relation") {
		t.Fatalf("internal error leaks the cause: %s", raw)
	}
}
