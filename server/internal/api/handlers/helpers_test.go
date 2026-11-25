package handlers

import (
	"transcoder/server/internal/api/middleware"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// asTestUser stands in for the session middleware: it authenticates the
// request as the user in the Test-User header, if it holds a valid id.
func asTestUser(c fiber.Ctx) error {
	if id, err := uuid.Parse(c.Get("Test-User")); err == nil {
		middleware.SetUserID(c, id)
	}
	return c.Next()
}
