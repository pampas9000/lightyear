package middleware

import (
	"errors"
	"strings"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/apikey"

	"github.com/gofiber/fiber/v3"
)

const apiKeyLocal = "api_key"

// APIKeyAuth authenticates `Authorization: Bearer <api key>` and sets the
// `user_id` local, like the session middleware, plus the key itself for
// RequireScope. Sessions are not accepted here.
func APIKeyAuth(keys *apikey.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		scheme, token, found := strings.Cut(c.Get(fiber.HeaderAuthorization), " ")
		token = strings.TrimSpace(token)
		if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
			return unauthorized(c, "Missing API key. Send 'Authorization: Bearer <key>'.")
		}
		key, err := keys.Authenticate(c.Context(), token)
		if err != nil {
			if errors.Is(err, apikey.ErrInvalidKey) {
				return unauthorized(c, "Invalid or revoked API key.")
			}
			return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to verify API key.")
		}
		c.Locals("user_id", key.OwnerID.String())
		c.Locals(apiKeyLocal, key)
		return c.Next()
	}
}

func unauthorized(c fiber.Ctx, message string) error {
	c.Set(fiber.HeaderWWWAuthenticate, `Bearer realm="lightyear"`)
	return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, message)
}

// RequireScope rejects requests whose API key lacks scope with 403.
func RequireScope(scope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		key, _ := c.Locals(apiKeyLocal).(*models.APIKey)
		if !apikey.HasScope(key, scope) {
			return response.RespondErrorWithDetails(c, fiber.StatusForbidden, response.CodeInsufficientScope,
				"API key lacks the required scope.", map[string]string{"required_scope": scope})
		}
		return c.Next()
	}
}
