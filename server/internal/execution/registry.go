package execution

import (
	"errors"
	"fmt"
	"slices"

	"transcoder/server/internal/operation"
	taskqueue "transcoder/server/internal/task"
)

var ErrUnavailable = errors.New("executor unavailable")

type Executor struct {
	Stream     string
	Operations []operation.Kind
}

// Registry contains only installed executors, not merely known provider names.
type Registry map[string]Executor

func DefaultRegistry() Registry {
	return Registry{
		"compute": {Stream: taskqueue.ComputeStreamKey, Operations: []operation.Kind{operation.Transcode}},
	}
}

func (r Registry) Resolve(name string, kind operation.Kind) (Executor, error) {
	e, ok := r[name]
	if !ok || e.Stream == "" || !slices.Contains(e.Operations, kind) {
		return Executor{}, fmt.Errorf("%w: %s cannot execute %s", ErrUnavailable, name, kind)
	}
	return e, nil
}
