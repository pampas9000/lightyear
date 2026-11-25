package api

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"transcoder/server/internal/config"
	"transcoder/server/internal/services/apikey"
	"transcoder/server/internal/services/storage"
	"transcoder/server/internal/testutil"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const testOrigin = "http://localhost:3000"

// envelopeApp is the production app (NewApp) on a test database. Redis is
// TEST_REDIS_URL when set, else an address nothing listens on, so session
// and rate-limit calls fail at once. The object store is unreachable too:
// a route that needs it answers a 500 envelope.
func envelopeApp(t *testing.T) (app *fiber.App, router *Router, haveRedis bool) {
	t.Helper()
	db := testutil.OpenDatabase(t)
	var kv *redis.Client
	if raw := os.Getenv("TEST_REDIS_URL"); raw != "" {
		opt, err := redis.ParseURL(raw)
		if err != nil {
			t.Fatalf("TEST_REDIS_URL: %v", err)
		}
		kv, haveRedis = redis.NewClient(opt), true
	} else {
		kv = redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	}
	t.Cleanup(func() { _ = kv.Close() })
	s3 := storage.NewS3Service(s3sdk.New(s3sdk.Options{
		Region:           "auto",
		Credentials:      credentials.NewStaticCredentialsProvider("id", "secret", ""),
		BaseEndpoint:     aws.String("http://127.0.0.1:1"),
		UsePathStyle:     true,
		RetryMaxAttempts: 1,
	}))
	cfg := config.Config{FrontendURL: testOrigin, S3: config.S3Config{Bucket: "bucket"}}
	router = NewRouter(db, kv, cfg, s3)
	return NewApp(router), router, haveRedis
}

// bodyless reports whether a response is allowed to carry no envelope:
// redirects, 304 and 204.
func bodyless(status int) bool {
	return status == http.StatusNoContent || status == http.StatusNotModified ||
		(status >= 300 && status < 400)
}

// checkEnvelope fails unless res is the envelope: exactly the five fields,
// "ok" on success and an error.* code otherwise.
func checkEnvelope(t *testing.T, label string, res *http.Response) map[string]json.RawMessage {
	t.Helper()
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if bodyless(res.StatusCode) {
		return nil
	}
	if ct := res.Header.Get(fiber.HeaderContentType); !strings.HasPrefix(ct, fiber.MIMEApplicationJSON) {
		t.Errorf("%s: %d Content-Type %q, body %.200s", label, res.StatusCode, ct, raw)
		return nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Errorf("%s: %d not a JSON object: %.200s", label, res.StatusCode, raw)
		return nil
	}
	if len(body) != 5 {
		t.Errorf("%s: %d fields %v, want success, code, message, data, details: %.200s", label, res.StatusCode, keys(body), raw)
	}
	for _, k := range []string{"success", "code", "message", "data", "details"} {
		if _, ok := body[k]; !ok {
			t.Errorf("%s: %d missing %q: %.200s", label, res.StatusCode, k, raw)
		}
	}
	var success bool
	var code string
	_ = json.Unmarshal(body["success"], &success)
	_ = json.Unmarshal(body["code"], &code)
	switch {
	case res.StatusCode < 400 && (!success || code != "ok"):
		t.Errorf("%s: %d success=%v code=%q, want true \"ok\"", label, res.StatusCode, success, code)
	case res.StatusCode >= 400 && (success || !strings.HasPrefix(code, "error.")):
		t.Errorf("%s: %d success=%v code=%q, want false error.*", label, res.StatusCode, success, code)
	}
	return body
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// routePath fills a registered route's parameters with values its handler
// can parse, so requests get past ID parsing.
func routePath(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		switch {
		case s == ":provider":
			segs[i] = "github"
		case s == ":partNumber" || s == ":part_number":
			segs[i] = "1"
		case strings.HasPrefix(s, ":"):
			segs[i] = uuid.NewString()
		}
	}
	return strings.Join(segs, "/")
}

func do(t *testing.T, app *fiber.App, req *http.Request) *http.Response {
	t.Helper()
	res, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	return res
}

// Every registered route, as an anonymous caller, a logged-in console user
// and an API key, answers with the envelope; so do unknown routes, a method
// a path does not have, an oversized body and a panic. /mcp speaks JSON-RPC
// and is left out.
func TestEveryRouteAnswersEnvelope(t *testing.T) {
	app, router, haveRedis := envelopeApp(t)
	app.Get("/test-panic", func(fiber.Ctx) error { panic("secret panic detail") })

	owner := uuid.New()
	_, apiKey, err := router.keys.Create(t.Context(), owner, "sweep", apikey.AllScopes)
	if err != nil {
		t.Fatal(err)
	}

	type caller struct {
		name    string
		headers map[string]string
	}
	callers := []caller{
		{"anonymous", nil},
		{"api-key", map[string]string{fiber.HeaderAuthorization: "Bearer " + apiKey, fiber.HeaderOrigin: testOrigin}},
	}
	if haveRedis {
		cookie := register(t, app)
		callers = append(callers, caller{"session", map[string]string{fiber.HeaderCookie: cookie, fiber.HeaderOrigin: testOrigin}})
	} else {
		t.Log("TEST_REDIS_URL not set: skipping the logged-in console pass")
	}

	// Logout ends the session, so it goes last.
	all := app.GetRoutes(true)
	last := func(r fiber.Route) int {
		if strings.HasSuffix(r.Path, "/logout") {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(all, func(a, b fiber.Route) int { return cmp.Compare(last(a), last(b)) })
	routes := 0
	for _, rt := range all {
		if rt.Method == fiber.MethodHead || strings.HasPrefix(rt.Path, "/mcp") || rt.Path == "/test-panic" {
			continue
		}
		routes++
		path := routePath(rt.Path)
		for _, c := range callers {
			var body io.Reader
			if rt.Method == fiber.MethodPost || rt.Method == fiber.MethodPut {
				body = strings.NewReader("{}")
			}
			req := httptest.NewRequest(rt.Method, path, body)
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			checkEnvelope(t, c.name+" "+rt.Method+" "+rt.Path, do(t, app, req))
		}
	}
	if routes < 40 {
		t.Fatalf("swept %d routes; the route table looks incomplete", routes)
	}

	expect := func(label string, req *http.Request, status int, code string) map[string]json.RawMessage {
		t.Helper()
		res := do(t, app, req)
		if res.StatusCode != status {
			t.Errorf("%s: status %d, want %d", label, res.StatusCode, status)
		}
		body := checkEnvelope(t, label, res)
		if got := strings.Trim(string(body["code"]), `"`); body != nil && got != code {
			t.Errorf("%s: code %s, want %s", label, got, code)
		}
		return body
	}

	expect("unknown route", httptest.NewRequest(http.MethodGet, "/nope", nil), http.StatusNotFound, "error.route.not_found")
	expect("unknown method", httptest.NewRequest(http.MethodPatch, "/test-panic", nil), http.StatusMethodNotAllowed, "error.route.method_not_allowed")

	// fasthttp rejects an oversized body before routing and app.Test
	// reports that as an error, so this one goes over a real connection.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	// The server answers from the Content-Length header alone, before the
	// body is read, so only the headers are sent.
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /v1/tasks HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n",
		apiKey, fiber.DefaultBodyLimit+1)
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: status %d, want 413", res.StatusCode)
	}
	if b := checkEnvelope(t, "oversized body", res); b != nil && string(b["code"]) != `"error.body.too_large"` {
		t.Errorf("oversized body: code %s", b["code"])
	}

	body := expect("panic", httptest.NewRequest(http.MethodGet, "/test-panic", nil), http.StatusInternalServerError, "error.internal")
	if strings.Contains(string(body["message"])+string(body["details"]), "secret") {
		t.Errorf("panic: the panic value leaked into the body: %s %s", body["message"], body["details"])
	}
	if !strings.Contains(string(body["details"]), "request_id") {
		t.Errorf("panic: details %s, want the request_id", body["details"])
	}
	if haveRedis {
		req := httptest.NewRequest(http.MethodGet, "/api/nope", nil)
		req.Header.Set(fiber.HeaderCookie, register(t, app))
		expect("unknown /api route", req, http.StatusNotFound, "error.route.not_found")
	}

	// Errors carry details: null rather than leaving it out.
	me := checkEnvelope(t, "details null", do(t, app, httptest.NewRequest(http.MethodGet, "/api/me", nil)))
	if me != nil && string(me["details"]) != "null" {
		t.Errorf("details %s, want null", me["details"])
	}
}

// register creates a console user and returns its session cookie.
func register(t *testing.T, app *fiber.App) string {
	t.Helper()
	name := "u" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	req := httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader(
		`{"username":"`+name+`","email":"`+name+`@example.com","password":"correct horse battery"}`))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderOrigin, testOrigin)
	res := do(t, app, req)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("register: %d %s", res.StatusCode, raw)
	}
	for _, c := range res.Cookies() {
		return c.Name + "=" + c.Value
	}
	t.Fatal("register: no session cookie")
	return ""
}
