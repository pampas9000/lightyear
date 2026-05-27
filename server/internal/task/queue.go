package task

import (
	"context"
	"encoding/json"

	"transcoder/server/internal/transcode"

	"github.com/redis/go-redis/v9"
)

const (
	JobStreamKey     = "transcoder:jobs:stream"
	ComputeStreamKey = "transcoder:compute:stream"
	ResultStreamKey  = "transcoder:results:stream"
)

type ComputePayload struct {
	SchemaVersion string           `json:"schema_version"`
	JobID         string           `json:"job_id"`
	AttemptID     string           `json:"attempt_id"`
	InputPath     string           `json:"input_path"`
	InputFormat   string           `json:"input_format,omitempty"`
	OutputPath    string           `json:"output_path"`
	TargetFormat  string           `json:"target_format"`
	Params        transcode.Params `json:"params"`
}

// Status of ResultPayload
//
// - COMPLETED: Transcode successfully
//
// - FAILED: Transcode failed
type ResultStatus string

const (
	ResultStatusCompleted  = ResultStatus("COMPLETED")
	ResultStatusFailed     = ResultStatus("FAILED")
	ResultStatusProcessing = ResultStatus("PROCESSING")
)

type ResultPayload struct {
	SchemaVersion string         `json:"schema_version"`
	JobID         string         `json:"job_id"`
	AttemptID     string         `json:"attempt_id"`
	Status        ResultStatus   `json:"status"`
	ErrorMessage  string         `json:"error_message,omitempty"`
	Progress      *int           `json:"progress,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

func EnqueueJob(ctx context.Context, kv *redis.Client, jobID string) error {
	return kv.XAdd(ctx, &redis.XAddArgs{
		Stream: JobStreamKey,
		Values: map[string]any{
			"job_id": jobID,
		},
	}).Err()
}

func EnqueueComputeJob(ctx context.Context, kv *redis.Client, payload *ComputePayload) error {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return kv.XAdd(ctx, &redis.XAddArgs{
		Stream: ComputeStreamKey,
		Values: map[string]any{
			"payload": string(bytes),
		},
	}).Err()
}

func EnqueueResult(ctx context.Context, kv *redis.Client, payload *ResultPayload) error {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return kv.XAdd(ctx, &redis.XAddArgs{
		Stream: ResultStreamKey,
		Values: map[string]any{
			"payload": string(bytes),
		},
	}).Err()
}
