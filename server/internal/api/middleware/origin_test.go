package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/session"
)

func TestNormalizeOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"http://localhost:3000":        "http://localhost:3000",
		"http://localhost:3000/":       "http://localhost:3000",
		"HTTPS://App.Example.com/path": "https://app.example.com",
		"https://example.com:443":      "https://example.com",
		"http://example.com:80/":       "http://example.com",
		"https://example.com:8443":     "https://example.com:8443",
		"http://[::1]:3000":            "http://[::1]:3000",
	} {
		got, err := NormalizeOrigin(raw)
		if err != nil || got != want {
			t.Errorf("NormalizeOrigin(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "localhost:3000", "/app", "ftp://example.com"} {
		if got, err := NormalizeOrigin(raw); err == nil {
			t.Errorf("NormalizeOrigin(%q) = %q, want an error", raw, got)
		}
	}
}

func TestRequireOrigin(t *testing.T) {
	app := fiber.New()
	ok := func(c fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) }
	api := app.Group("/api", RequireOrigin("http://localhost:3000"))
	api.Post("/login", ok)
	api.Get("/me", ok)
	api.Delete("/api-keys/:id", ok)
	app.Post("/v1/tasks", ok)
	app.Post("/mcp", ok)

	call := func(method, path, origin string) int {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	cases := []struct {
		method, path, origin string
		want                 int
	}{
		{http.MethodPost, "/api/login", "http://localhost:3000", 204},
		{http.MethodDelete, "/api/api-keys/1", "http://localhost:3000", 204},
		{http.MethodPost, "/api/login", "https://evil.example", 403},
		{http.MethodPost, "/api/login", "http://localhost:3000.evil.example", 403},
		{http.MethodPost, "/api/login", "http://localhost:3001", 403},
		{http.MethodPost, "/api/login", "null", 403},
		{http.MethodPost, "/api/login", "", 403},
		{http.MethodDelete, "/api/api-keys/1", "", 403},
		{http.MethodGet, "/api/me", "", 204},
		{http.MethodGet, "/api/me", "https://evil.example", 204},
		{http.MethodPost, "/v1/tasks", "", 204},
		{http.MethodPost, "/v1/tasks", "https://evil.example", 204},
		{http.MethodPost, "/mcp", "", 204},
	}
	for _, tc := range cases {
		if got := call(tc.method, tc.path, tc.origin); got != tc.want {
			t.Errorf("%s %s Origin=%q: %d, want %d", tc.method, tc.path, tc.origin, got, tc.want)
		}
	}
}

func TestAuthMiddlewareIgnoresBearer(t *testing.T) {
	store := session.NewStore()
	app := fiber.New()
	app.Post("/login", func(c fiber.Ctx) error {
		sess, err := store.Get(c)
		if err != nil {
			return err
		}
		sess.Set("user_id", "u-1")
		if err := sess.Save(); err != nil {
			return err
		}
		return c.SendString(sess.ID())
	})
	app.Get("/api/me", AuthMiddleware(store), func(c fiber.Ctx) error {
		return c.SendString(c.Locals("user_id").(string))
	})

	res, err := app.Test(httptest.NewRequest(http.MethodPost, "/login", nil))
	if err != nil {
		t.Fatal(err)
	}
	var sid string
	for _, ck := range res.Cookies() {
		if ck.Name == "session_id" {
			sid = ck.Value
		}
	}
	res.Body.Close()
	if sid == "" {
		t.Fatal("login set no session_id cookie")
	}

	me := func(set func(*http.Request)) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		set(req)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if got := me(func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session_id", Value: sid}) }); got != 200 {
		t.Fatalf("cookie: %d, want 200", got)
	}
	if got := me(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+sid) }); got != 401 {
		t.Fatalf("Bearer session ID: %d, want 401", got)
	}
	if got := me(func(*http.Request) {}); got != 401 {
		t.Fatalf("no credentials: %d, want 401", got)
	}
}
