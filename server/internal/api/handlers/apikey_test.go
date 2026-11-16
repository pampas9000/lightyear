package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"transcoder/server/internal/services/apikey"
	"transcoder/server/internal/testutil"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func TestAPIKeyEndpoints(t *testing.T) {
	db := testutil.OpenDatabase(t)
	keys := apikey.NewService(db)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", c.Get("Test-User"))
		return c.Next()
	})
	NewAPIKeyHandler(keys).Install(app.Group("/api"))
	owner := uuid.New()

	call := func(method, path, body string, user uuid.UUID) (int, string) {
		t.Helper()
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, r)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Test-User", user.String())
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(raw)
	}

	status, raw := call(http.MethodPost, "/api/api-keys", `{"name":"ci","scopes":["tasks:read"]}`, owner)
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, raw)
	}
	var created struct {
		Data struct {
			Key    string `json:"key"`
			APIKey struct {
				ID     uuid.UUID `json:"id"`
				Prefix string    `json:"prefix"`
				Scopes []string  `json:"scopes"`
			} `json:"api_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &created); err != nil {
		t.Fatal(err)
	}
	plaintext := created.Data.Key
	if !strings.HasPrefix(plaintext, created.Data.APIKey.Prefix) || len(created.Data.APIKey.Scopes) != 1 || strings.Contains(raw, "key_hash") {
		t.Fatalf("create response: %s", raw)
	}

	status, raw = call(http.MethodGet, "/api/api-keys", "", owner)
	if status != http.StatusOK || !strings.Contains(raw, created.Data.APIKey.Prefix) || strings.Contains(raw, plaintext) || strings.Contains(raw, apikey.Hash(plaintext)) {
		t.Fatalf("list must show the prefix only: %d %s", status, raw)
	}
	if status, raw = call(http.MethodGet, "/api/api-keys", "", uuid.New()); status != http.StatusOK || strings.Contains(raw, created.Data.APIKey.Prefix) {
		t.Fatalf("another user sees the key: %s", raw)
	}

	for body, want := range map[string]int{`{"name":""}`: 400, `{"name":"x","scopes":["admin"]}`: 400, `nope`: 400} {
		if status, raw := call(http.MethodPost, "/api/api-keys", body, owner); status != want {
			t.Errorf("%s: %d %s", body, status, raw)
		}
	}

	path := "/api/api-keys/" + created.Data.APIKey.ID.String()
	if status, _ := call(http.MethodDelete, path, "", uuid.New()); status != http.StatusNotFound {
		t.Fatalf("another user revoked the key: %d", status)
	}
	if status, raw := call(http.MethodDelete, path, "", owner); status != http.StatusOK || !strings.Contains(raw, `"revoked_at":"`) {
		t.Fatalf("revoke: %d %s", status, raw)
	}
	if _, err := keys.Authenticate(t.Context(), plaintext); err == nil {
		t.Fatal("revoked key still authenticates")
	}
}
