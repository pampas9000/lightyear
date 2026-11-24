package orchestrator_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"transcoder/server/internal/services/orchestrator"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// countCommands counts the commands a client sends, by name.
type countCommands struct {
	mu    sync.Mutex
	count map[string]int
}

func (c *countCommands) DialHook(next redis.DialHook) redis.DialHook { return next }
func (c *countCommands) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (c *countCommands) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		c.mu.Lock()
		c.count[strings.ToLower(cmd.Name())]++
		c.mu.Unlock()
		return next(ctx, cmd)
	}
}

func (c *countCommands) get(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count[name]
}

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	kv := redis.NewClient(opts)
	t.Cleanup(func() { _ = kv.Close() })
	return kv
}

func TestConsumeReadsInBatchesAndClaimsAtMostOncePerInterval(t *testing.T) {
	kv := testRedis(t)
	stream, group := "test:results:"+uuid.NewString(), "orchestrators"
	t.Cleanup(func() { kv.Del(context.Background(), stream) })
	if err := kv.XGroupCreateMkStream(t.Context(), stream, group, "0").Err(); err != nil {
		t.Fatal(err)
	}
	for i := range 45 {
		kv.XAdd(t.Context(), &redis.XAddArgs{Stream: stream, Values: map[string]any{"payload": fmt.Sprint(i)}})
	}
	counts := &countCommands{count: map[string]int{}}
	kv.AddHook(counts)

	ctx, cancel := context.WithCancel(t.Context())
	var handled []string
	var done sync.WaitGroup
	done.Go(func() {
		orchestrator.Consume(ctx, kv, stream, group, "c1", func(msg redis.XMessage) error {
			handled = append(handled, msg.Values["payload"].(string))
			if len(handled) == 45 {
				// Let the loop run a few more idle reads.
				time.AfterFunc(2500*time.Millisecond, cancel)
			}
			return nil
		})
	})
	done.Wait()
	for i, p := range handled {
		if p != fmt.Sprint(i) {
			t.Fatalf("entries out of order: %v", handled)
		}
	}
	if len(handled) != 45 {
		t.Fatalf("handled %d entries", len(handled))
	}
	if n := counts.get("xautoclaim"); n != 1 {
		t.Fatalf("XAUTOCLAIM sent %d times in one interval", n)
	}
	// 3 reads with entries (20+20+5), then about one empty read per second.
	if n := counts.get("xreadgroup"); n < 4 || n > 7 {
		t.Fatalf("XREADGROUP sent %d times", n)
	}
	if pending := kv.XPending(t.Context(), stream, group).Val(); pending.Count != 0 {
		t.Fatalf("%d entries left unacknowledged", pending.Count)
	}
}

func TestConsumeTakesOverEntriesADeadConsumerLeft(t *testing.T) {
	orchestrator.SetAutoClaim(t, 200*time.Millisecond, 100*time.Millisecond)
	kv := testRedis(t)
	stream, group := "test:results:"+uuid.NewString(), "orchestrators"
	t.Cleanup(func() { kv.Del(context.Background(), stream) })
	if err := kv.XGroupCreateMkStream(t.Context(), stream, group, "0").Err(); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		kv.XAdd(t.Context(), &redis.XAddArgs{Stream: stream, Values: map[string]any{"payload": fmt.Sprint(i)}})
	}
	// A consumer reads them and dies without acknowledging.
	if err := kv.XReadGroup(t.Context(), &redis.XReadGroupArgs{Group: group, Consumer: "dead", Streams: []string{stream, ">"}}).Err(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var handled atomic.Int32
	var failedOnce atomic.Bool
	orchestrator.Consume(ctx, kv, stream, group, "c2", func(msg redis.XMessage) error {
		// The first attempt at one entry fails; it is claimed again later.
		if msg.Values["payload"] == "1" && failedOnce.CompareAndSwap(false, true) {
			return fmt.Errorf("database down")
		}
		if handled.Add(1) == 3 {
			// After this entry's acknowledgement.
			time.AfterFunc(300*time.Millisecond, cancel)
		}
		return nil
	})
	if handled.Load() != 3 || !failedOnce.Load() {
		t.Fatalf("handled %d of 3 left entries", handled.Load())
	}
	if pending := kv.XPending(t.Context(), stream, group).Val(); pending.Count != 0 {
		t.Fatalf("%d entries left unacknowledged", pending.Count)
	}
}
