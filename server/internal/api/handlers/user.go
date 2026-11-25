package handlers

import (
	"errors"

	"transcoder/server/internal/api/response"

	"github.com/gofiber/fiber/v3"
)

// errNoUser: a handler behind the session middleware ran without an
// authenticated user (middleware.UserID); a wiring mistake, answered 401.
var errNoUser = errors.New("user id not found in context")

// respondNoUser answers a protected request that has no authenticated user.
func respondNoUser(c fiber.Ctx) error {
	return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context.")
}
