package task

import (
	"context"
	"encoding/json"

	"transcoder/server/internal/operation"
	"transcoder/server/internal/transcode"

	"github.com/redis/go-redis/v9"
)

const (
	JobStreamKey           = "transcoder:jobs:stream"
	ComputeStreamKey       = "transcoder:compute:stream"
	ResultStreamKey        = "transcoder:results:stream"
	StreamMaxLen           = 100000
	JobCancelKeyPrefix     = "transcoder:cancel:job:"
	TaskCancelKeyPrefix    = "transcoder:cancel:task:"
	AttemptCancelKeyPrefix = "transcoder:cancel:attempt:"
)

type Input struct {
	FileID string `json:"file_id"`
	Role   string `json:"role"`
	Path   string `json:"path"`
}

type Output struct {
	FileID string `json:"file_id"`
	Role   string `json:"role"`
	Path   string `json:"path"`
}

type ExecutionPayload struct {
	SchemaVersion string         `json:"schema_version"`
	JobID         string         `json:"job_id"`
	AttemptID     string         `json:"attempt_id"`
	Generation    int            `json:"generation"`
	Operation     operation.Kind `json:"operation"`
	Executor      string         `json:"executor"`
	Params        operation.JSON `json:"operation_params"`
	Inputs        []Input        `json:"inputs"`
	Outputs       []Output       `json:"outputs"`
	TargetFormat  string         `json:"target_format"`
}

type ComputePayload struct {
	SchemaVersion       string           `json:"schema_version"`
	JobID               string           `json:"job_id"`
	AttemptID           string           `json:"attempt_id"`
	InputPath           string           `json:"input_path"`
	InputFormat         string           `json:"input_format,omitempty"`
	OutputPath          string           `json:"output_path"`
	TargetFormat        string           `json:"target_format"`
	Params              transcode.Params `json:"params"`
	OutputFileID        string           `json:"output_file_id,omitempty"`
	AttemptScopedCancel bool             `json:"attempt_scoped_cancel,omitzero"`
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
	SchemaVersion     string         `json:"schema_version"`
	JobID             string         `json:"job_id"`
	AttemptID         string         `json:"attempt_id"`
	Status            ResultStatus   `json:"status"`
	ErrorMessage      string         `json:"error_message,omitempty"`
	ErrorCode         string         `json:"error_code,omitempty"`
	Retryable         *bool          `json:"retryable,omitempty"`
	Progress          *int           `json:"progress,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	Artifacts         []Artifact     `json:"artifacts,omitempty"`
	RetryAfterSeconds *int           `json:"retry_after_seconds,omitempty"`
}

type Artifact struct {
	FileID   string `json:"file_id"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	MimeType string `json:"mime_type"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

func EnqueueJob(ctx context.Context, kv *redis.Client, jobID string) error {
	return kv.XAdd(ctx, &redis.XAddArgs{
		Stream: JobStreamKey,
		MaxLen: StreamMaxLen,
		Approx: true,
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
		MaxLen: StreamMaxLen,
		Approx: true,
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
		MaxLen: StreamMaxLen,
		Approx: true,
		Values: map[string]any{
			"payload": string(bytes),
		},
	}).Err()
}
