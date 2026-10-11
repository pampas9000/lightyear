package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/operation"
	taskqueue "transcoder/server/internal/task"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func Add(tx *gorm.DB, kind, destination string, payload any, at time.Time) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return tx.Create(&models.OutboxEvent{
		Kind: kind, Destination: destination, Payload: operation.JSON(raw), AvailableAt: at,
	}).Error
}

func EnqueueJob(tx *gorm.DB, jobID uuid.UUID, at time.Time) error {
	return Add(tx, "stream", taskqueue.JobStreamKey, map[string]any{"job_id": jobID.String()}, at)
}

func CancelAttempt(tx *gorm.DB, attemptID uuid.UUID) error {
	return Add(tx, "cancel", taskqueue.AttemptCancelKeyPrefix+attemptID.String(), map[string]any{"value": "1"}, time.Now())
}

type Publisher interface {
	Publish(context.Context, models.OutboxEvent) error
}

type RedisPublisher struct {
	Client *redis.Client
}

func (p RedisPublisher) Publish(ctx context.Context, e models.OutboxEvent) error {
	if e.Kind == "cancel" {
		return p.Client.Set(ctx, e.Destination, "1", 24*time.Hour).Err()
	}
	if e.Kind != "stream" {
		return fmt.Errorf("unsupported outbox event kind %q", e.Kind)
	}
	var values map[string]any
	if err := json.Unmarshal(e.Payload, &values); err != nil {
		return err
	}
	return p.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: e.Destination, MaxLen: taskqueue.StreamMaxLen, Approx: true, Values: values,
	}).Err()
}

type Service struct {
	db        *gorm.DB
	publisher Publisher
}

func NewService(db *gorm.DB, publisher Publisher) *Service {
	return &Service{db: db, publisher: publisher}
}

func RetryDelay(retries int) time.Duration {
	return time.Second * time.Duration(1<<min(max(retries, 0), 8))
}

// Relay may repeat a publish if the process dies before the DB commit. Consumers
// must deduplicate; the outbox does not promise exactly-once external execution.
func (s *Service) Relay(ctx context.Context, limit int) (int, error) {
	published := 0
	for range limit {
		var publishErr error
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var event models.OutboxEvent
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
				Where("published_at IS NULL AND available_at <= ?", time.Now()).
				Order("available_at, id").First(&event).Error; err != nil {
				return err
			}
			publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			publishErr = s.publisher.Publish(publishCtx, event)
			if publishErr != nil {
				return tx.Model(&event).Updates(map[string]any{
					"retry_count":  event.RetryCount + 1,
					"available_at": time.Now().Add(RetryDelay(event.RetryCount)),
					"last_error":   publishErr.Error(),
				}).Error
			}
			return tx.Model(&event).Updates(map[string]any{"published_at": time.Now(), "last_error": ""}).Error
		})
		if errors.Is(err, gorm.ErrRecordNotFound) {
			break
		}
		if err != nil {
			return published, err
		}
		if publishErr != nil {
			return published, publishErr
		}
		published++
	}
	return published, nil
}
