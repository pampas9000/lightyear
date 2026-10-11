package providers

import (
	"context"
	"errors"
	"time"

	"transcoder/server/internal/operation"
	taskqueue "transcoder/server/internal/task"
)

var (
	ErrReconciliationRequired = errors.New("external submission requires reconciliation")
	ErrAlreadySubmitted       = errors.New("external request already submitted")
	ErrInactiveAttempt        = errors.New("job attempt is no longer active")
)

type Request struct {
	IdempotencyKey string
	Operation      operation.Kind
	Params         operation.JSON
	Inputs         []taskqueue.Input
}

type Usage struct {
	CostMinor int64  `json:"cost_minor"`
	Currency  string `json:"currency"`
}

type Receipt struct {
	RemoteTaskID string
	RequestID    string
	NextPollAt   *time.Time
}

type Outcome struct {
	Done      bool
	Failed    bool
	Artifacts []taskqueue.Artifact
	Usage     *Usage
}

// Adapters must document whether Submit honors the stable idempotency key.
// Lookup must not report "absent" unless it can prove no request was accepted.
type Provider interface {
	Submit(context.Context, Request) (Receipt, error)
	Poll(context.Context, Receipt) (Outcome, error)
	Lookup(context.Context, string) (Receipt, bool, error)
}
