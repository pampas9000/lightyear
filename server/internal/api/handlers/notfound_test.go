package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/models"
	"transcoder/server/internal/testutil"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// A missing (or another user's) workflow or file answers 404 with its
// not_found code, not "ok".
func TestNotFoundCodes(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner, other := uuid.New(), uuid.New()
	wf := models.Workflow{OwnerID: other, Name: "theirs"}
	if err := db.Create(&wf).Error; err != nil {
		t.Fatal(err)
	}
	file := models.File{OwnerID: other, Name: "a.png", Path: "uploads/a.png", Status: models.FileUploaded}
	if err := db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Use(asTestUser)
	api := app.Group("/api")
	NewWorkflowHandler(db).Install(api)
	NewFileHandler(db, nil, "bucket", nil).Install(api)

	update := `{"name":"mine","options":{"format":"webp"}}`
	for _, tc := range []struct {
		method, path, body string
		code               response.ResponseCode
	}{
		{http.MethodGet, "/api/workflows/" + uuid.NewString(), "", response.CodeWorkflowNotFound},
		{http.MethodGet, "/api/workflows/" + wf.ID.String(), "", response.CodeWorkflowNotFound},
		{http.MethodPut, "/api/workflows/" + wf.ID.String(), update, response.CodeWorkflowNotFound},
		{http.MethodDelete, "/api/workflows/" + wf.ID.String(), "", response.CodeWorkflowNotFound},
		{http.MethodGet, "/api/files/" + uuid.NewString() + "/download-url", "", response.CodeFileNotFound},
		{http.MethodGet, "/api/files/" + file.ID.String() + "/download-url", "", response.CodeFileNotFound},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Test-User", owner.String())
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound || body.Code != tc.code.String() {
			t.Errorf("%s %s: %d %q, want 404 %q", tc.method, tc.path, res.StatusCode, body.Code, tc.code)
		}
	}
}

// Handlers behind the session middleware answer 401 when no user was set.
func TestHandlersWithoutUserAnswer401(t *testing.T) {
	db := testutil.OpenDatabase(t)
	app := fiber.New()
	NewWorkflowHandler(db).Install(app.Group("/api"))
	res, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/workflows", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no user: %d, want 401", res.StatusCode)
	}
}
