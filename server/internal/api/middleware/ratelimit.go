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

// Take takes a token from user's bucket. It returns 0 when the request may
// go ahead, or else the whole seconds to wait. When the limiter itself fails
// (Redis down) the request goes through: rate limiting protects capacity,
// and quotas, which live in Postgres, still apply. A nil limiter allows
// everything. The MCP server shares these buckets with /v1.
func Take(ctx context.Context, l Limiter, user string) (retryAfter int) {
	if l == nil {
		return 0
	}
	ok, wait, err := l.Allow(ctx, user)
	if err != nil {
		now := time.Now().UnixNano()
		if last := lastFailOpenWarn.Load(); now-last >= int64(failOpenLogEvery) && lastFailOpenWarn.CompareAndSwap(last, now) {
			slog.Warn("rate limiter unavailable, allowing requests", "error", err)
		}
		return 0
	}
	if ok {
		return 0
	}
	return max(int(math.Ceil(wait.Seconds())), 1)
}

var lastFailOpenWarn atomic.Int64

// RateLimit takes a token from the user's bucket (the user_id local, so all
// of a user's API keys share it) and answers 429 with Retry-After when it is
// empty. See Take for a limiter that fails.
func RateLimit(l Limiter) fiber.Handler {
	return func(c fiber.Ctx) error {
		user := fmt.Sprint(c.Locals("user_id"))
		seconds := Take(c.Context(), l, user)
		if seconds == 0 {
			return c.Next()
		}
		prefix := ""
		if key, _ := c.Locals(apiKeyLocal).(*models.APIKey); key != nil {
			prefix = key.Prefix
		}
		slog.Warn("rate limited", "user_id", user, "api_key_prefix", prefix,
			"method", c.Method(), "route", c.Route().Path, "retry_after", seconds)
		c.Set(fiber.HeaderRetryAfter, strconv.Itoa(seconds))
		return response.RespondErrorWithDetails(c, fiber.StatusTooManyRequests, response.CodeRateLimited,
			"Too many requests. Retry after the time in Retry-After.", RateLimitDetail{RetryAfter: seconds})
	}
}

// RateLimitDetail is the details of error.rate_limit.exceeded.
type RateLimitDetail struct {
	RetryAfter int `json:"retry_after"`
}

// APIKey returns the key APIKeyAuth authenticated, or nil.
func APIKey(c fiber.Ctx) *models.APIKey {
	key, _ := c.Locals(apiKeyLocal).(*models.APIKey)
	return key
}
