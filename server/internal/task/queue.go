// Package task holds the queue contract shared with workers. The Rust side
// mirrors it in compute-types' protocol module; both run the fixtures in
// docs/contracts/fixtures/{dispatch,results}-v2.json.
package task

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"transcoder/server/internal/compression"
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
	Target    *compression.Target  `json:"target,omitempty"`
	SourceKey string               `json:"source_key"`
	// SourceURL, when set, is fetched by the worker and written to SourceKey
	// before processing: the source file is FETCHING.
	SourceURL string `json:"source_url,omitempty"`
	OutputKey string `json:"output_key"`
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

// FetchedSource reports a source the worker fetched from Dispatch.SourceURL
// and stored at Key (the dispatch's source_key).
type FetchedSource struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
	Mime string `json:"mime"`
}

type ResultError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type Result struct {
	V               int                   `json:"v"`
	JobID           string                `json:"job_id"`
	AttemptID       string                `json:"attempt_id"`
	Status          ResultStatus          `json:"status"`
	Progress        *int                  `json:"progress,omitempty"`
	Artifact        *Artifact             `json:"artifact,omitempty"`
	ResolvedOptions *imageoptions.Options `json:"resolved_options,omitempty"`
	TargetMet       *bool                 `json:"target_met,omitempty"`
	Achieved        *compression.Achieved `json:"achieved,omitempty"`
	EncodeRounds    *int                  `json:"encode_rounds,omitempty"`
	ComputeMS       *int64                `json:"compute_ms,omitempty"`
	// Source is set once a dispatched source_url has been fetched and stored:
	// on a RUNNING message sent right after the fetch and on the outcome.
	Source *FetchedSource `json:"source,omitempty"`
	Error  *ResultError   `json:"error,omitempty"`
}

// Queue is what the orchestrator needs from Redis.
type Queue interface {
	// Send appends payload to queue and returns the entry's stream ID.
	Send(ctx context.Context, queue string, payload []byte) (string, error)
	Cancel(ctx context.Context, attemptID string) error
	// Waiting reports whether the entry id is still in queue and has not been
	// delivered to WorkerGroup yet, i.e. a worker will still read it. It is
	// false when the entry was delivered, trimmed or lost with the stream or
	// group. An error means Redis could not answer.
	Waiting(ctx context.Context, queue, id string) (bool, error)
}

type RedisQueue struct{ Client *redis.Client }

func (q RedisQueue) Send(ctx context.Context, queue string, payload []byte) (string, error) {
	return q.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: queue, MaxLen: StreamMaxLen, Approx: true,
		Values: map[string]any{"payload": string(payload)},
	}).Result()
}

func (q RedisQueue) Cancel(ctx context.Context, attemptID string) error {
	return q.Client.Set(ctx, CancelKeyPrefix+attemptID, "1", cancelTTL).Err()
}

// Waiting uses XRANGE for the exact entry and the group's last-delivered-id
// from XINFO GROUPS (both Redis 5+). Workers read with NOACK, so an entry at
// or before last-delivered-id has been handed to a worker.
func (q RedisQueue) Waiting(ctx context.Context, queue, id string) (bool, error) {
	entries, err := q.Client.XRange(ctx, queue, id, id).Result()
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return false, nil
	}
	groups, err := q.Client.XInfoGroups(ctx, queue).Result()
	if err != nil {
		// The stream went away between the two calls.
		if isMissing(err) {
			return false, nil
		}
		return false, err
	}
	for _, g := range groups {
		if g.Name == WorkerGroup {
			return StreamIDAfter(id, g.LastDeliveredID)
		}
	}
	return false, nil
}

func isMissing(err error) bool {
	msg := err.Error()
	return strings.HasPrefix(msg, "NOGROUP") || strings.Contains(msg, "no such key")
}

// ParseStreamID splits a stream ID "<ms>-<seq>" into its two numbers.
func ParseStreamID(id string) (ms, seq uint64, err error) {
	left, right, ok := strings.Cut(id, "-")
	if !ok {
		return 0, 0, fmt.Errorf("%w: %q", errBadStreamID, id)
	}
	if ms, err = strconv.ParseUint(left, 10, 64); err != nil {
		return 0, 0, fmt.Errorf("%w: %q", errBadStreamID, id)
	}
	if seq, err = strconv.ParseUint(right, 10, 64); err != nil {
		return 0, 0, fmt.Errorf("%w: %q", errBadStreamID, id)
	}
	return ms, seq, nil
}

var errBadStreamID = errors.New("invalid stream id")

// StreamIDAfter reports whether stream ID a sorts after b. IDs compare as
// (ms, seq) number pairs; as strings "1-10" would sort before "1-2".
func StreamIDAfter(a, b string) (bool, error) {
	ams, aseq, err := ParseStreamID(a)
	if err != nil {
		return false, err
	}
	bms, bseq, err := ParseStreamID(b)
	if err != nil {
		return false, err
	}
	return ams > bms || (ams == bms && aseq > bseq), nil
}
