package middleware

import (
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// userKey is the Locals key of the authenticated user's id. It is unexported
// so the id can only be set through SetUserID, already parsed.
type userKey struct{}

// SetUserID records the authenticated user for the rest of the request. The
// session, API key and JWT middlewares call it.
func SetUserID(c fiber.Ctx, id uuid.UUID) {
	c.Locals(userKey{}, id)
}

// UserID is the authenticated user's id, set by SetUserID. ok is false on
// routes no authentication middleware ran on.
func UserID(c fiber.Ctx) (id uuid.UUID, ok bool) {
	id, ok = c.Locals(userKey{}).(uuid.UUID)
	return id, ok && id != uuid.Nil
}
