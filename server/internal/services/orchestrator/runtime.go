package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"transcoder/server/internal/execution"
	"transcoder/server/internal/services/outbox"
	taskqueue "transcoder/server/internal/task"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func Run(ctx context.Context, db *gorm.DB, kv *redis.Client) error {
	for stream, group := range map[string]string{
		taskqueue.JobStreamKey:    "transcoder:jobs:group",
		taskqueue.ResultStreamKey: "transcoder:results:group",
	} {
		if err := kv.XGroupCreateMkStream(ctx, stream, group, "0").Err(); err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
			return fmt.Errorf("create consumer group: %w", err)
		}
	}
	host, _ := os.Hostname()
	consumer := fmt.Sprintf("orchestrator-%s-%d-%s", host, os.Getpid(), uuid.NewString())
	service := NewService(db, execution.DefaultRegistry())
	relay := outbox.NewService(db, outbox.RedisPublisher{Client: kv})
	var wg sync.WaitGroup
	wg.Go(func() {
		consume(ctx, kv, taskqueue.JobStreamKey, "transcoder:jobs:group", consumer, func(msg redis.XMessage) error {
			value, ok := msg.Values["job_id"].(string)
			if !ok {
				return ErrInvalidResult
			}
			id, err := uuid.Parse(value)
			if err != nil {
				return ErrInvalidResult
			}
			return service.Dispatch(ctx, id)
		})
	})
	wg.Go(func() {
		consume(ctx, kv, taskqueue.ResultStreamKey, "transcoder:results:group", consumer, func(msg redis.XMessage) error {
			raw, ok := msg.Values["payload"].(string)
			if !ok {
				return ErrInvalidResult
			}
			var result taskqueue.ResultPayload
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				return ErrInvalidResult
			}
			return service.ProcessResult(ctx, result)
		})
	})
	wg.Go(func() {
		for {
			if _, err := relay.Relay(ctx, 100); err != nil && ctx.Err() == nil {
				slog.Error("outbox relay failed", "error", err)
			}
			if !wait(ctx, 500*time.Millisecond) {
				return
			}
		}
	})
	wg.Go(func() {
		for {
			if err := service.Recover(ctx, time.Now()); err != nil && ctx.Err() == nil {
				slog.Error("job recovery failed", "error", err)
			}
			if !wait(ctx, 30*time.Second) {
				return
			}
		}
	})
	<-ctx.Done()
	wg.Wait()
	return nil
}

func consume(ctx context.Context, kv *redis.Client, stream, group, consumer string, handle func(redis.XMessage) error) {
	cursor := "0-0"
	for ctx.Err() == nil {
		entries, next, err := kv.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: stream, Group: group, Consumer: consumer, MinIdle: 30 * time.Second, Start: cursor, Count: 1,
		}).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			if ctx.Err() == nil {
				slog.Error("claim stream entries failed", "stream", stream, "error", err)
			}
			if !wait(ctx, time.Second) {
				return
			}
			continue
		}
		cursor = next
		if cursor == "" {
			cursor = "0-0"
		}
		if len(entries) == 0 {
			streams, err := kv.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group: group, Consumer: consumer, Streams: []string{stream, ">"}, Count: 1, Block: time.Second,
			}).Result()
			if err != nil {
				if !errors.Is(err, redis.Nil) && ctx.Err() == nil {
					slog.Error("read stream failed", "stream", stream, "error", err)
					if !wait(ctx, time.Second) {
						return
					}
				}
				continue
			}
			for _, value := range streams {
				entries = append(entries, value.Messages...)
			}
		}
		for _, msg := range entries {
			err := handle(msg)
			if err != nil && !errors.Is(err, ErrInvalidResult) && !errors.Is(err, gorm.ErrRecordNotFound) {
				slog.Error("stream entry failed", "stream", stream, "message_id", msg.ID, "error", err)
				continue
			}
			if err != nil {
				slog.Warn("dropping invalid stream entry", "stream", stream, "message_id", msg.ID, "error", err)
			}
			if err := kv.XAck(ctx, stream, group, msg.ID).Err(); err != nil && ctx.Err() == nil {
				slog.Error("ack stream failed", "stream", stream, "message_id", msg.ID, "error", err)
			}
		}
	}
}

func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
