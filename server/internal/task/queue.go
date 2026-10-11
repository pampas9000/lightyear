// Package task holds the queue contract shared with workers. The Rust side
// mirrors it in compute-types' protocol module; both run the fixtures in
// docs/contracts/fixtures/{dispatch,results}-v2.json.
package task

import (
	"context"
	"time"

	"transcoder/server/internal/imageoptions"

	"github.com/redis/go-redis/v9"
)

const (
	Version = 2
	// QueueImage carries image.convert dispatches to image workers.
	QueueImage  = "q:image"
	WorkerGroup = "workers"
	// QueueResults carries worker heartbeats and outcomes to orchestrators.
	QueueResults      = "q:results"
	OrchestratorGroup = "orchestrators"
	StreamMaxLen      = 100000
	// CancelKeyPrefix + attempt ID marks an attempt the worker should abandon.
	CancelKeyPrefix = "cancel:attempt:"
	cancelTTL       = 24 * time.Hour
)

type Dispatch struct {
	V         int                  `json:"v"`
	JobID     string               `json:"job_id"`
	AttemptID string               `json:"attempt_id"`
	Op        string               `json:"op"`
	Options   imageoptions.Options `json:"options"`
	SourceKey string               `json:"source_key"`
	OutputKey string               `json:"output_key"`
}

type ResultStatus string

const (
	ResultRunning   ResultStatus = "RUNNING"
	ResultCompleted ResultStatus = "COMPLETED"
	ResultFailed    ResultStatus = "FAILED"
)

type Artifact struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	Mime   string `json:"mime"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type ResultError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type Result struct {
	V         int          `json:"v"`
	JobID     string       `json:"job_id"`
	AttemptID string       `json:"attempt_id"`
	Status    ResultStatus `json:"status"`
	Progress  *int         `json:"progress,omitempty"`
	Artifact  *Artifact    `json:"artifact,omitempty"`
	Error     *ResultError `json:"error,omitempty"`
}

// Queue is what the orchestrator needs from Redis.
type Queue interface {
	Send(ctx context.Context, queue string, payload []byte) error
	Cancel(ctx context.Context, attemptID string) error
}

type RedisQueue struct{ Client *redis.Client }

func (q RedisQueue) Send(ctx context.Context, queue string, payload []byte) error {
	return q.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: queue, MaxLen: StreamMaxLen, Approx: true,
		Values: map[string]any{"payload": string(payload)},
	}).Err()
}

func (q RedisQueue) Cancel(ctx context.Context, attemptID string) error {
	return q.Client.Set(ctx, CancelKeyPrefix+attemptID, "1", cancelTTL).Err()
}
