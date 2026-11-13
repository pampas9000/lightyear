package workflow

import (
	"encoding/json"
	"errors"
	"testing"

	"transcoder/server/internal/compression"
	"transcoder/server/internal/imageoptions"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
)

func TestCreateAndUpdateWorkflowOutputs(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	svc := NewService(db)

	multi, err := svc.CreateWorkflow(t.Context(), CreateWorkflowInput{
		OwnerID: owner,
		Name:    "responsive",
		Outputs: json.RawMessage(`[
			{"name":"preview","options":{"format":"avif"},"target":{"max_bytes":50000}},
			{"name":"fallback","options":{"format":"webp","quality":80}}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(multi.Outputs) != 2 || multi.Outputs[0].Name != "preview" || multi.Outputs[0].Options.Quality != nil {
		t.Fatalf("unexpected workflow outputs: %+v", multi.Outputs)
	}

	// Legacy single-options shorthand creates a 1-output workflow.
	single, err := svc.CreateWorkflow(t.Context(), CreateWorkflowInput{
		OwnerID: owner,
		Name:    "legacy-like",
		Options: json.RawMessage(`{"format":"webp"}`),
		Target:  json.RawMessage(`{"max_bytes":40000}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(single.Outputs) != 1 || single.Outputs[0].Options.Format != imageoptions.FormatWebP || single.Outputs[0].Target == nil {
		t.Fatalf("unexpected single workflow outputs: %+v", single.Outputs)
	}

	// Combining outputs and options is rejected.
	if _, err := svc.CreateWorkflow(t.Context(), CreateWorkflowInput{
		OwnerID: owner,
		Name:    "ambiguous",
		Outputs: json.RawMessage(`[{"options":{"format":"avif"}}]`),
		Options: json.RawMessage(`{"format":"webp"}`),
	}); !errors.Is(err, ErrOutputsRequired) {
		t.Fatalf("expected ErrOutputsRequired, got %v", err)
	}

	// PNG + target rejected inside workflow outputs.
	if _, err := svc.CreateWorkflow(t.Context(), CreateWorkflowInput{
		OwnerID: owner,
		Name:    "bad-png",
		Outputs: json.RawMessage(`[{"options":{"format":"png"},"target":{"max_bytes":1000}}]`),
	}); !errors.Is(err, compression.ErrInvalidTarget) {
		t.Fatalf("expected ErrInvalidTarget, got %v", err)
	}
}
