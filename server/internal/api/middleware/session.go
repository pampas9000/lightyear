package middleware

import (
	"strings"

	"transcoder/server/internal/api/response"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/session"
)

// AuthMiddleware creates a middleware that checks for a valid session (either via Cookie or Bearer token).
func AuthMiddleware(store *session.Store) fiber.Handler {
	return func(c fiber.Ctx) error {
		var sess *session.Session
		var err error

		// 1. Try Bearer Token (Session ID)
		authHeader := c.Get("Authorization")
		if token, found := strings.CutPrefix(authHeader, "Bearer "); found {
			sess, err = store.GetByID(c.Context(), token)
		} else {
			// 2. Try Cookie
			sess, err = store.Get(c)
		}

		if err == nil && sess != nil {
			userID := sess.Get("user_id")
			if userID != nil {
				c.Locals("user_id", userID)
				return c.Next()
			}
		}

		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "Unauthorized access. Missing session or valid token.")
	}
}
