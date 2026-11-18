package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync/atomic"
	"time"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/models"

	"github.com/gofiber/fiber/v3"
)

// Limiter is the token bucket RateLimit consults (ratelimit.Limiter).
type Limiter interface {
	Allow(ctx context.Context, key string) (bool, time.Duration, error)
}

// failOpenLogEvery throttles the warning logged while the limiter is down.
const failOpenLogEvery = time.Minute

// RateLimit takes a token from the user's bucket (the user_id local, so all
// of a user's API keys share it) and answers 429 with Retry-After when it is
// empty. When the limiter itself fails (Redis down) the request goes through:
// rate limiting protects capacity, and quotas, which live in Postgres, still
// apply. A nil limiter allows everything.
func RateLimit(l Limiter) fiber.Handler {
	var lastWarn atomic.Int64
	return func(c fiber.Ctx) error {
		if l == nil {
			return c.Next()
		}
		user := fmt.Sprint(c.Locals("user_id"))
		ok, wait, err := l.Allow(c.Context(), user)
		if err != nil {
			now := time.Now().UnixNano()
			if last := lastWarn.Load(); now-last >= int64(failOpenLogEvery) && lastWarn.CompareAndSwap(last, now) {
				slog.Warn("rate limiter unavailable, allowing requests", "error", err)
			}
			return c.Next()
		}
		if ok {
			return c.Next()
		}
		seconds := max(int(math.Ceil(wait.Seconds())), 1)
		prefix := ""
		if key, _ := c.Locals(apiKeyLocal).(*models.APIKey); key != nil {
			prefix = key.Prefix
		}
		slog.Warn("rate limited", "user_id", user, "api_key_prefix", prefix,
			"method", c.Method(), "route", c.Route().Path, "retry_after", seconds)
		c.Set(fiber.HeaderRetryAfter, strconv.Itoa(seconds))
		return response.RespondErrorWithDetails(c, fiber.StatusTooManyRequests, response.CodeRateLimited,
			"Too many requests. Retry after the time in Retry-After.", map[string]int{"retry_after": seconds})
	}
}
