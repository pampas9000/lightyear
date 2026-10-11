package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type publisherFunc func(context.Context, models.OutboxEvent) error

func (f publisherFunc) Publish(ctx context.Context, e models.OutboxEvent) error { return f(ctx, e) }

func TestOutboxRollsBackWithBusinessMutation(t *testing.T) {
	db := testutil.OpenDatabase(t)
	err := db.Transaction(func(tx *gorm.DB) error {
		job := models.Job{OwnerID: uuid.New()}
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		if err := EnqueueJob(tx, job.ID, time.Now()); err != nil {
			return err
		}
		return errors.New("business mutation failed")
	})
	if err == nil {
		t.Fatal("transaction unexpectedly succeeded")
	}
	for _, model := range []any{&models.Job{}, &models.OutboxEvent{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("rolled-back data leaked: count=%d err=%v", count, err)
		}
	}
}

func TestRelayRetriesPublishFailure(t *testing.T) {
	db := testutil.OpenDatabase(t)
	if err := EnqueueJob(db, uuid.New(), time.Now()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s := NewService(db, publisherFunc(func(context.Context, models.OutboxEvent) error {
		calls++
		if calls == 1 {
			return errors.New("redis unavailable")
		}
		return nil
	}))
	if n, err := s.Relay(t.Context(), 10); n != 0 || err == nil {
		t.Fatalf("expected retained publish failure: n=%d err=%v", n, err)
	}
	var event models.OutboxEvent
	db.First(&event)
	if event.PublishedAt != nil || event.RetryCount != 1 || event.LastError == "" {
		t.Fatalf("failed event was not retained: %+v", event)
	}
	db.Model(&event).Update("available_at", time.Now().Add(-time.Second))
	if n, err := s.Relay(t.Context(), 10); n != 1 || err != nil {
		t.Fatalf("retry: n=%d err=%v", n, err)
	}
}

func TestConcurrentRelaysClaimDifferentEvents(t *testing.T) {
	db := testutil.OpenDatabase(t)
	for range 12 {
		if err := EnqueueJob(db, uuid.New(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	delivered := map[uuid.UUID]int{}
	s := NewService(db, publisherFunc(func(_ context.Context, event models.OutboxEvent) error {
		mu.Lock()
		defer mu.Unlock()
		delivered[event.ID]++
		return nil
	}))
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if _, err := s.Relay(t.Context(), 12); err != nil {
				t.Errorf("relay: %v", err)
			}
		})
	}
	wg.Wait()
	if len(delivered) != 12 {
		t.Fatalf("expected 12 deliveries, got %d", len(delivered))
	}
	for _, count := range delivered {
		if count != 1 {
			t.Errorf("event was concurrently delivered %d times", count)
		}
	}
}
