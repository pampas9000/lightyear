package middleware

import (
	"fmt"
	"net/url"
	"strings"

	"transcoder/server/internal/api/response"

	"github.com/gofiber/fiber/v3"
)

// NormalizeOrigin reduces an absolute URL such as FRONTEND_URL to the
// scheme://host[:port] form browsers send in the Origin header: lowercase,
// no path, and no default port.
func NormalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse %q: %w", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("%q is not an absolute http(s) URL", raw)
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6 literal
	}
	if port := u.Port(); port != "" && !(scheme == "http" && port == "80") && !(scheme == "https" && port == "443") {
		host += ":" + port
	}
	return scheme + "://" + host, nil
}

// RequireOrigin rejects state-changing requests (POST, PUT, PATCH, DELETE)
// whose Origin header is not exactly allowed, the output of NormalizeOrigin.
// A missing Origin is rejected too. It guards the cookie-authenticated /api
// routes, including login and logout, against cross-site requests; /v1 and
// /mcp authenticate with API keys and do not use it.
func RequireOrigin(allowed string) fiber.Handler {
	return func(c fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodPost, fiber.MethodPut, fiber.MethodPatch, fiber.MethodDelete:
		default:
			return c.Next()
		}
		if origin := c.Get(fiber.HeaderOrigin); origin != "" && origin == allowed {
			return c.Next()
		}
		return response.RespondError(c, fiber.StatusForbidden, response.CodeOriginRejected, "Cross-origin request rejected.")
	}
}
