package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"transcoder/server/internal/models"
	"transcoder/server/internal/testutil"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func TestJobEndpointDoesNotExposeAnotherOwnersJob(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	job := models.Job{OwnerID: owner}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", c.Get("Test-User"))
		return c.Next()
	})
	NewHandler(db, nil).Install(app.Group("/api"))
	for _, test := range []struct {
		user   uuid.UUID
		status int
	}{{owner, http.StatusOK}, {uuid.New(), http.StatusNotFound}} {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+job.ID.String(), nil)
		req.Header.Set("Test-User", test.user.String())
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != test.status {
			t.Errorf("expected %d, got %d", test.status, res.StatusCode)
		}
		res.Body.Close()
	}
}
