package ratelimit

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// openRedis connects to TEST_REDIS_URL, a disposable local Redis.
func openRedis(t *testing.T) *redis.Client {
	t.Helper()
	raw := os.Getenv("TEST_REDIS_URL")
	if raw == "" {
		t.Skip("set TEST_REDIS_URL to run Redis integration tests")
	}
	opt, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	kv := redis.NewClient(opt)
	t.Cleanup(func() { _ = kv.Close() })
	return kv
}

func TestBucketAllowsBurstThenRefills(t *testing.T) {
	kv := openRedis(t)
	prefix := "test:rl:" + uuid.NewString() + ":"
	t.Cleanup(func() {
		keys, _ := kv.Keys(t.Context(), prefix+"*").Result()
		if len(keys) > 0 {
			kv.Del(t.Context(), keys...)
		}
	})
	l := New(kv, prefix, 60, 3) // one token a second, three at once
	for i := range 3 {
		ok, _, err := l.Allow(t.Context(), "u1")
		if err != nil || !ok {
			t.Fatalf("call %d of the burst refused: %v", i+1, err)
		}
	}
	ok, wait, err := l.Allow(t.Context(), "u1")
	if err != nil || ok || wait <= 0 || wait > time.Second {
		t.Fatalf("want a refusal with a wait up to 1s, got ok=%v wait=%v err=%v", ok, wait, err)
	}
	if ok, _, _ := l.Allow(t.Context(), "u2"); !ok {
		t.Fatal("buckets must be per key")
	}

	fast := New(kv, prefix+"fast:", 60_000, 1) // a token every millisecond
	if ok, _, _ := fast.Allow(t.Context(), "u"); !ok {
		t.Fatal("first call refused")
	}
	time.Sleep(5 * time.Millisecond)
	if ok, _, _ := fast.Allow(t.Context(), "u"); !ok {
		t.Fatal("bucket did not refill")
	}
	ttl, err := kv.PTTL(t.Context(), prefix+"u1").Result()
	if err != nil || ttl <= 0 || ttl > 5*time.Second {
		t.Fatalf("bucket keys must expire once full again, ttl %v", ttl)
	}
}

func TestDisabledAndUnreachable(t *testing.T) {
	if l := New(nil, "x", 0, 10); l != nil {
		t.Fatal("perMinute 0 must disable the limiter")
	}
	var off *Limiter
	if ok, _, err := off.Allow(t.Context(), "u"); !ok || err != nil {
		t.Fatal("a nil limiter must allow")
	}
	down := New(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1}), "x", 60, 1)
	if _, _, err := down.Allow(t.Context(), "u"); err == nil {
		t.Fatal("an unreachable Redis must be reported")
	}
}
