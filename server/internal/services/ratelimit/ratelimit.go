// Package ratelimit is a token bucket kept in Redis, shared by every API
// instance. Redis only counts here: losing the buckets resets them to full,
// and nothing else depends on them.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// bucket refills at rate tokens per millisecond up to burst and takes one
// token per call. Time comes from the Redis server, so instances with skewed
// clocks share one bucket correctly. Returns {allowed, wait_ms}.
var bucket = redis.NewScript(`
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local state = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(state[1]) or burst
local ts = tonumber(state[2]) or now
tokens = math.min(burst, tokens + math.max(0, now - ts) * rate)
local allowed, wait = 0, 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  wait = math.ceil((1 - tokens) / rate)
end
redis.call('HSET', KEYS[1], 'tokens', tostring(tokens), 'ts', now)
redis.call('PEXPIRE', KEYS[1], math.ceil(burst / rate) + 1000)
return {allowed, wait}
`)

// Limiter allows PerMinute calls per key on average and Burst at once.
type Limiter struct {
	kv     *redis.Client
	prefix string
	rate   float64 // tokens per millisecond
	burst  int
}

// New returns nil, meaning no limit, when perMinute is 0. burst defaults to
// 1 when not positive.
func New(kv *redis.Client, prefix string, perMinute, burst int) *Limiter {
	if perMinute <= 0 {
		return nil
	}
	return &Limiter{kv: kv, prefix: prefix, rate: float64(perMinute) / 60_000, burst: max(burst, 1)}
}

// Allow takes a token from key's bucket. When none is left it reports how
// long until one is. A nil Limiter allows everything.
func (l *Limiter) Allow(ctx context.Context, key string) (bool, time.Duration, error) {
	if l == nil {
		return true, 0, nil
	}
	res, err := bucket.Run(ctx, l.kv, []string{l.prefix + key}, l.rate, l.burst).Int64Slice()
	if err != nil {
		return false, 0, fmt.Errorf("rate limit: %w", err)
	}
	if len(res) != 2 {
		return false, 0, fmt.Errorf("rate limit: unexpected reply %v", res)
	}
	return res[0] == 1, time.Duration(res[1]) * time.Millisecond, nil
}
