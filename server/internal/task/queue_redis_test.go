package task

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestStreamIDAfterComparesNumbers(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"5-10", "5-2", true},
		{"5-2", "5-10", false},
		{"10-0", "9-99", true},
		{"9-99", "10-0", false},
		{"5-3", "5-3", false},
		{"1-0", "0-0", true},
		{"18446744073709551615-18446744073709551615", "18446744073709551615-18446744073709551614", true},
	}
	for _, c := range cases {
		got, err := StreamIDAfter(c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("StreamIDAfter(%q, %q) = %v, %v; want %v", c.a, c.b, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "5", "5-", "-1", "a-1", "1-b", "-1-1", "1-2-3"} {
		if _, err := StreamIDAfter(bad, "0-0"); err == nil {
			t.Errorf("StreamIDAfter(%q) accepted a malformed id", bad)
		}
	}
}

// TestRedisQueueWaiting runs against TEST_REDIS_URL (a disposable local
// Redis); it uses its own stream and deletes it afterwards.
func TestRedisQueueWaiting(t *testing.T) {
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
	ctx := t.Context()
	stream := "test:image:" + uuid.NewString()
	t.Cleanup(func() { kv.Del(context.Background(), stream) })
	q := RedisQueue{Client: kv}

	if ok, err := q.Waiting(ctx, stream, "5-2"); ok || err != nil {
		t.Fatalf("missing stream: %v %v", ok, err)
	}
	for _, id := range []string{"5-2", "5-10", "5-11"} {
		if err := kv.XAdd(ctx, &redis.XAddArgs{Stream: stream, ID: id, Values: map[string]any{"payload": "{}"}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := q.Waiting(ctx, stream, "5-2"); ok || err != nil {
		t.Fatalf("no workers group yet: %v %v", ok, err)
	}
	if err := kv.XGroupCreate(ctx, stream, WorkerGroup, "0").Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := q.Waiting(ctx, stream, "5-2"); !ok || err != nil {
		t.Fatalf("undelivered entry: %v %v", ok, err)
	}
	if err := kv.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: WorkerGroup, Consumer: "c", Streams: []string{stream, ">"}, Count: 1, NoAck: true,
	}).Err(); err != nil {
		t.Fatal(err)
	}
	// last-delivered-id is now 5-2; "5-10" < "5-2" as strings.
	if ok, err := q.Waiting(ctx, stream, "5-2"); ok || err != nil {
		t.Fatalf("delivered entry: %v %v", ok, err)
	}
	if ok, err := q.Waiting(ctx, stream, "5-10"); !ok || err != nil {
		t.Fatalf("5-10 comes after 5-2: %v %v", ok, err)
	}
	if err := kv.XDel(ctx, stream, "5-10").Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := q.Waiting(ctx, stream, "5-10"); ok || err != nil {
		t.Fatalf("trimmed entry: %v %v", ok, err)
	}
	if err := kv.XGroupDestroy(ctx, stream, WorkerGroup).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := q.Waiting(ctx, stream, "5-11"); ok || err != nil {
		t.Fatalf("group gone: %v %v", ok, err)
	}
	id, err := q.Send(ctx, stream, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseStreamID(id); err != nil {
		t.Fatalf("Send returned %q: %v", id, err)
	}
}
