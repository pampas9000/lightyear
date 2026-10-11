package task

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"transcoder/server/internal/operation"

	"github.com/google/uuid"
)

type InputSpec struct {
	FileID uuid.UUID `json:"file_id"`
	Role   string    `json:"role"`
}

type JobSpec struct {
	Operation    operation.Kind `json:"operation"`
	Executor     string         `json:"executor"`
	Inputs       []InputSpec    `json:"inputs"`
	TargetFormat string         `json:"target_format"`
	Params       operation.JSON `json:"operation_params"`
	outputCount  int
}

func normalizeSpec(spec JobSpec) (JobSpec, error) {
	if spec.Operation == "" {
		spec.Operation = operation.Transcode
	}
	if spec.Executor == "" && spec.Operation == operation.Transcode {
		spec.Executor = "compute"
	}
	spec.TargetFormat = strings.ToUpper(strings.TrimSpace(spec.TargetFormat))
	if spec.TargetFormat == "JPG" {
		spec.TargetFormat = "JPEG"
	}
	if !slices.Contains([]string{"AVIF", "JXL", "HEIC", "JPEG", "PNG", "WEBP"}, spec.TargetFormat) {
		return JobSpec{}, fmt.Errorf("%w: unsupported target_format", operation.ErrInvalid)
	}
	roles := make([]string, len(spec.Inputs))
	for i, input := range spec.Inputs {
		if input.FileID == uuid.Nil {
			return JobSpec{}, ErrInvalidFileID
		}
		roles[i] = input.Role
	}
	if err := operation.ValidateInputs(spec.Operation, roles); err != nil {
		return JobSpec{}, err
	}
	params, count, err := operation.Normalize(spec.Operation, spec.Params, spec.TargetFormat)
	if err != nil {
		return JobSpec{}, err
	}
	spec.Params, spec.outputCount = params, count
	return spec, nil
}

func encodeSpecs(specs []JobSpec) ([]byte, error) {
	// A task is a batch; the order of jobs is not part of its business identity.
	items := make([]string, len(specs))
	for i, spec := range specs {
		raw, err := json.Marshal(spec)
		if err != nil {
			return nil, err
		}
		items[i] = string(raw)
	}
	slices.Sort(items)
	return json.Marshal(items)
}
