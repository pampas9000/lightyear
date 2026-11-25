package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"transcoder/server/internal/services/lifecycle"
	taskqueue "transcoder/server/internal/task"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func Run(ctx context.Context, db *gorm.DB, kv *redis.Client, limits Limits, retention lifecycle.Policy) error {
	if err := ensureGroup(ctx, kv, taskqueue.QueueResults, taskqueue.OrchestratorGroup); err != nil {
		return fmt.Errorf("create consumer group: %w", err)
	}
	host, _ := os.Hostname()
	consumer := fmt.Sprintf("orchestrator-%s-%d-%s", host, os.Getpid(), uuid.NewString())
	service := NewService(db, taskqueue.RedisQueue{Client: kv}, limits, retention)
	var wg sync.WaitGroup
	wg.Go(func() {
		consume(ctx, kv, taskqueue.QueueResults, taskqueue.OrchestratorGroup, consumer, func(msg redis.XMessage) error {
			raw, ok := msg.Values["payload"].(string)
			if !ok {
				return ErrInvalidResult
			}
			var result taskqueue.Result
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				return ErrInvalidResult
			}
			return service.ProcessResult(ctx, result)
		})
	})
	wg.Go(func() {
		dispatchLoop(ctx, service.DispatchDue, func(ctx context.Context) bool { return wait(ctx, DispatchInterval) })
	})
	wg.Go(func() {
		for {
			if _, err := service.Relay(ctx, time.Now()); err != nil && ctx.Err() == nil {
				slog.Error("outbox relay failed", "error", err)
			}
			if !wait(ctx, time.Second) {
				return
			}
		}
	})
	wg.Go(func() {
		for {
			if err := service.Recover(ctx, time.Now()); err != nil && ctx.Err() == nil {
				slog.Error("job recovery failed", "error", err)
			}
			if !wait(ctx, 15*time.Second) {
				return
			}
		}
	})
	wg.Go(func() {
		for {
			if n, err := service.PruneOutbox(ctx, time.Now()); err != nil && ctx.Err() == nil {
				slog.Error("outbox prune failed", "error", err)
			} else if n > 0 {
				slog.Info("pruned sent outbox messages", "count", n)
			}
			if !wait(ctx, PruneInterval) {
				return
			}
		}
	})
	<-ctx.Done()
	wg.Wait()
	return nil
}

// DispatchInterval is the pause between dispatch rounds that do not fetch the
// next batch at once (see dispatchAgain).
const DispatchInterval = 500 * time.Millisecond

// dispatchLoop runs dispatch rounds until ctx ends or pause returns false.
func dispatchLoop(ctx context.Context, dispatch func(context.Context, time.Time) (int, int, error), pause func(context.Context) bool) {
	for ctx.Err() == nil {
		claimed, candidates, err := dispatch(ctx, time.Now())
		if err != nil && ctx.Err() == nil {
			slog.Error("dispatch failed", "error", err)
		}
		if err == nil && dispatchAgain(claimed, candidates) {
			continue
		}
		if !pause(ctx) {
			return
		}
	}
}

// readBatch is how many results one read takes.
const readBatch = 20

// Variables so tests can shorten them.
var (
	// autoClaimInterval bounds how often consume scans for results another
	// consumer left unacknowledged.
	autoClaimInterval = 15 * time.Second
	// autoClaimIdle is how long a result stays unacknowledged before any
	// consumer may take it over.
	autoClaimIdle = 30 * time.Second
)

// consume handles stream entries one at a time, in order, in this goroutine.
// New entries are read readBatch at a time. Every autoClaimInterval it also
// takes over entries left unacknowledged for autoClaimIdle (by a consumer
// that died, or by this one when handling failed); a scan that stops short
// of the end resumes on the next pass instead of waiting for the interval.
func consume(ctx context.Context, kv *redis.Client, stream, group, consumer string, handle func(redis.XMessage) error) {
	cursor := "0-0"
	var lastClaim time.Time
	for ctx.Err() == nil {
		var entries []redis.XMessage
		var err error
		if cursor != "0-0" || time.Since(lastClaim) >= autoClaimInterval {
			var next string
			entries, next, err = kv.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream: stream, Group: group, Consumer: consumer, MinIdle: autoClaimIdle, Start: cursor, Count: readBatch,
			}).Result()
			if err == nil || errors.Is(err, redis.Nil) {
				lastClaim = time.Now()
				cursor = next
				if cursor == "" {
					cursor = "0-0"
				}
				err = nil
			}
		}
		if err == nil && len(entries) == 0 {
			var streams []redis.XStream
			streams, err = kv.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group: group, Consumer: consumer, Streams: []string{stream, ">"}, Count: readBatch, Block: time.Second,
			}).Result()
			if errors.Is(err, redis.Nil) {
				continue
			}
			for _, value := range streams {
				entries = append(entries, value.Messages...)
			}
		}
		if err != nil && redis.HasErrorPrefix(err, "NOGROUP") {
			// Redis lost the stream (restart without persistence). Results
			// in it are gone; leases recover the affected jobs.
			slog.Warn("consumer group missing; recreating", "stream", stream)
			cursor = "0-0"
			if err := ensureGroup(ctx, kv, stream, group); err != nil && ctx.Err() == nil {
				slog.Error("recreate consumer group failed", "stream", stream, "error", err)
				if !wait(ctx, time.Second) {
					return
				}
			}
			continue
		}
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("read stream failed", "stream", stream, "error", err)
			}
			if !wait(ctx, time.Second) {
				return
			}
			continue
		}
		for _, msg := range entries {
			if ctx.Err() != nil {
				return
			}
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

func ensureGroup(ctx context.Context, kv *redis.Client, stream, group string) error {
	err := kv.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && !redis.HasErrorPrefix(err, "BUSYGROUP") {
		return err
	}
	return nil
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
