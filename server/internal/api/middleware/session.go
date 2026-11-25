package middleware

import (
	"transcoder/server/internal/api/response"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/session"
	"github.com/google/uuid"
)

// AuthMiddleware requires a logged-in session and records its user with
// SetUserID. The session ID is read only from the HttpOnly session cookie:
// /api accepts no Authorization header, so a session ID never reaches page
// scripts and cannot be sent as a bearer token.
func AuthMiddleware(store *session.Store) fiber.Handler {
	return func(c fiber.Ctx) error {
		sess, err := store.Get(c)
		if err == nil && sess != nil {
			if raw, ok := sess.Get("user_id").(string); ok {
				if userID, err := uuid.Parse(raw); err == nil && userID != uuid.Nil {
					SetUserID(c, userID)
					return c.Next()
				}
			}
		}

		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "Unauthorized access. Missing or invalid session.")
	}
}
