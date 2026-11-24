package orchestrator_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"transcoder/server/internal/services/orchestrator"

	"github.com/google/uuid"
)

type round struct {
	claimed, candidates int
	err                 error
}

// runLoop feeds rounds to the dispatch loop and returns what it did: "d" for
// a dispatch round, "p" for a pause between rounds.
func runLoop(t *testing.T, rounds ...round) string {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var log []byte
	next := 0
	dispatch := func(context.Context, time.Time) (int, int, error) {
		if next == len(rounds) {
			cancel()
			return 0, 0, context.Canceled
		}
		r := rounds[next]
		next++
		log = append(log, 'd')
		return r.claimed, r.candidates, r.err
	}
	pause := func(ctx context.Context) bool {
		if ctx.Err() != nil {
			return false
		}
		log = append(log, 'p')
		return true
	}
	orchestrator.DispatchLoop(ctx, dispatch, pause)
	return string(log)
}

func TestDispatchLoopRefetchesOnlyAfterAFullBatchWithClaims(t *testing.T) {
	full := orchestrator.DispatchBatch
	cases := []struct {
		name   string
		rounds []round
		want   string
	}{
		{"full batches then a short one", []round{{full, full, nil}, {3, full, nil}, {5, 5, nil}}, "dddp"},
		{"full batch of skipped candidates", []round{{0, full, nil}, {0, full, nil}}, "dpdp"},
		{"short batch", []round{{4, 4, nil}, {0, 0, nil}}, "dpdp"},
		{"error after claims", []round{{2, full, errors.New("db down")}}, "dp"},
	}
	for _, c := range cases {
		if got := runLoop(t, c.rounds...); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDispatchDueReportsItsCandidates(t *testing.T) {
	f := newFixture(t, orchestrator.Limits{})
	owners := orchestrator.DispatchBatch + 5
	for range owners {
		f.submit(t, uuid.New(), 1)
	}
	var got [][2]int
	for range 3 {
		claimed, candidates, err := f.svc.DispatchDue(t.Context(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, [2]int{claimed, candidates})
	}
	want := [][2]int{{orchestrator.DispatchBatch, orchestrator.DispatchBatch}, {5, 5}, {0, 0}}
	if !slices.Equal(got, want) {
		t.Fatalf("rounds %v, want %v", got, want)
	}
}
