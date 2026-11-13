// Package compression defines compression targets, multi-output specs,
// artifact keys for target search, and compression ratio/filename helpers.
package compression

import (
	"bytes"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"transcoder/server/internal/imageoptions"
)

const (
	MaxItems     = 100
	MaxOutputs   = 10
	MaxTotalJobs = 200
)

var (
	ErrInvalidTarget  = errors.New("invalid compression target")
	ErrInvalidOutputs = errors.New("invalid outputs")
	ErrTooManyOutputs = errors.New("too many outputs")
	ErrTooManyJobs    = errors.New("too many total jobs")

	namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// Target constrains how an image should be compressed without prescribing
// the exact quality or Butteraugli distance.
type Target struct {
	MaxBytes *int64 `json:"max_bytes,omitempty"`
}

// IsEmpty reports whether t specifies no target constraints.
func (t *Target) IsEmpty() bool {
	return t == nil || t.MaxBytes == nil
}

// Value stores a target as JSONB.
func (t Target) Value() (driver.Value, error) {
	encoded, err := json.Marshal(t)
	return string(encoded), err
}

// Scan reads a JSONB target column.
func (t *Target) Scan(value any) error {
	if value == nil {
		*t = Target{}
		return nil
	}
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, t)
	case string:
		return json.Unmarshal([]byte(v), t)
	default:
		return fmt.Errorf("scan target: unsupported %T", value)
	}
}

// Achieved records measured metrics produced by encoding or search.
type Achieved struct {
	Bytes *int64 `json:"bytes,omitempty"`
}

// Value stores achieved metrics as JSONB.
func (a Achieved) Value() (driver.Value, error) {
	encoded, err := json.Marshal(a)
	return string(encoded), err
}

// Scan reads a JSONB achieved column.
func (a *Achieved) Scan(value any) error {
	if value == nil {
		*a = Achieved{}
		return nil
	}
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, a)
	case string:
		return json.Unmarshal([]byte(v), a)
	default:
		return fmt.Errorf("scan achieved: unsupported %T", value)
	}
}

// Stats summarizes the compression ratio of a completed job for HTTP responses.
type Stats struct {
	SourceBytes      int64   `json:"source_bytes"`
	OutputBytes      int64   `json:"output_bytes"`
	Ratio            float64 `json:"ratio"`
	SavedRatio       float64 `json:"saved_ratio"`
	LargerThanSource bool    `json:"larger_than_source"`
}

// OutputInput is the raw JSON shape of one entry in outputs[].
type OutputInput struct {
	Name    string          `json:"name,omitempty"`
	Options json.RawMessage `json:"options"`
	Target  json.RawMessage `json:"target,omitempty"`
}

// OutputSpec is a validated, canonicalized output specification.
type OutputSpec struct {
	Name    string               `json:"name,omitempty"`
	Options imageoptions.Options `json:"options"`
	Target  *Target              `json:"target,omitempty"`
}

// WorkflowOutputs stores a slice of OutputSpec as JSONB on workflows.
type WorkflowOutputs []OutputSpec

// Value stores workflow outputs as JSONB.
func (w WorkflowOutputs) Value() (driver.Value, error) {
	encoded, err := json.Marshal(w)
	return string(encoded), err
}

// Scan reads JSONB workflow outputs.
func (w *WorkflowOutputs) Scan(value any) error {
	if value == nil {
		*w = nil
		return nil
	}
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, w)
	case string:
		return json.Unmarshal([]byte(v), w)
	default:
		return fmt.Errorf("scan workflow outputs: unsupported %T", value)
	}
}

// ParseTarget strictly decodes an optional target JSON object.
func ParseTarget(raw json.RawMessage) (*Target, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTarget, err)
	}
	if v, ok := obj["max_bytes"]; ok && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return nil, fmt.Errorf("%w: max_bytes cannot be null", ErrInvalidTarget)
	}
	var t Target
	d := json.NewDecoder(bytes.NewReader(trimmed))
	d.DisallowUnknownFields()
	if err := d.Decode(&t); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTarget, err)
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: unexpected trailing JSON", ErrInvalidTarget)
	}
	if t.IsEmpty() {
		return nil, fmt.Errorf("%w: target cannot be empty", ErrInvalidTarget)
	}
	if *t.MaxBytes <= 0 {
		return nil, fmt.Errorf("%w: target.max_bytes must be greater than 0", ErrInvalidTarget)
	}
	return &t, nil
}

// ValidateTarget ensures target and options are structurally compatible.
func ValidateTarget(opts imageoptions.Options, target *Target) error {
	if target == nil {
		return nil
	}
	if target.IsEmpty() {
		return fmt.Errorf("%w: target cannot be empty", ErrInvalidTarget)
	}
	if *target.MaxBytes <= 0 {
		return fmt.Errorf("%w: target.max_bytes must be greater than 0", ErrInvalidTarget)
	}
	if opts.Format == imageoptions.FormatPNG {
		return fmt.Errorf("%w: png has no quality setting for target", ErrInvalidTarget)
	}
	return nil
}

// NeedsSearch reports whether worker iterative search is required instead of
// a single deterministic encode.
func NeedsSearch(opts imageoptions.Options, target *Target) bool {
	return target != nil && !target.IsEmpty() && opts.Quality == nil && opts.Distance == nil && !opts.Lossless
}

// ArtifactKey computes the deduplication key for (source, options, target).
// Fixed single-pass jobs keep the standard image-options/v2 key so they can
// share artifacts with non-target requests.
func ArtifactKey(source string, canonical imageoptions.Options, target *Target) (string, error) {
	if !NeedsSearch(canonical, target) {
		return imageoptions.ArtifactKey(source, canonical)
	}
	optBytes, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	targetBytes, err := json.Marshal(target)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte("compression-target/v1\n" + source + "\n" + string(optBytes) + "\n" + string(targetBytes)))
	return hex.EncodeToString(sum[:]), nil
}

// IsTargetMet evaluates whether outputBytes meets target, or nil without target.
func IsTargetMet(outputBytes int64, target *Target) *bool {
	if target == nil || target.MaxBytes == nil {
		return nil
	}
	met := outputBytes <= *target.MaxBytes
	return &met
}

// BuildStats computes compression telemetry from positive byte counts.
func BuildStats(sourceBytes, outputBytes int64) *Stats {
	if sourceBytes <= 0 || outputBytes <= 0 {
		return nil
	}
	ratio := float64(outputBytes) / float64(sourceBytes)
	return &Stats{
		SourceBytes:      sourceBytes,
		OutputBytes:      outputBytes,
		Ratio:            ratio,
		SavedRatio:       1 - ratio,
		LargerThanSource: outputBytes > sourceBytes,
	}
}

// Extension returns the canonical file extension for format.
func Extension(format imageoptions.Format) string {
	if format == imageoptions.FormatJPEG {
		return "jpg"
	}
	return string(format)
}

// OutputFilename derives the output filename from sourceName, format, and
// multi-output identity without clobbering sibling outputs in the same task.
func OutputFilename(sourceName string, format imageoptions.Format, outputName string, outputIndex int, multiOutput bool) string {
	stem := strings.TrimSuffix(sourceName, filepath.Ext(sourceName))
	ext := Extension(format)
	if !multiOutput {
		return stem + "." + ext
	}
	label := outputName
	if label == "" {
		label = strconv.Itoa(outputIndex + 1)
	}
	return stem + "." + label + "." + ext
}

// ParseSingleOutput validates an explicit single options+target pair.
func ParseSingleOutput(optionsRaw, targetRaw json.RawMessage) (OutputSpec, error) {
	return ParseOutput(OutputInput{Options: optionsRaw, Target: targetRaw})
}

// HasJSONValue reports whether raw contains non-empty JSON other than null.
func HasJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// ParseOutputsJSON strictly decodes and validates a JSON outputs array.
func ParseOutputsJSON(raw json.RawMessage) ([]OutputSpec, error) {
	trimmed := bytes.TrimSpace(raw)
	var inputs []OutputInput
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&inputs); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutputs, err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: unexpected trailing JSON", ErrInvalidOutputs)
	}
	return ParseOutputs(inputs)
}

// ParseOutput validates one OutputInput entry.
func ParseOutput(raw OutputInput) (OutputSpec, error) {
	if raw.Name != "" && !namePattern.MatchString(raw.Name) {
		return OutputSpec{}, fmt.Errorf("%w: name %q must match [A-Za-z0-9_-]{1,64}", ErrInvalidOutputs, raw.Name)
	}
	if len(bytes.TrimSpace(raw.Options)) == 0 || bytes.Equal(bytes.TrimSpace(raw.Options), []byte("null")) {
		return OutputSpec{}, fmt.Errorf("%w: options is required", ErrInvalidOutputs)
	}
	target, err := ParseTarget(raw.Target)
	if err != nil {
		return OutputSpec{}, err
	}
	var opts imageoptions.Options
	if target != nil {
		opts, err = imageoptions.ParseForTarget(raw.Options)
	} else {
		opts, err = imageoptions.Parse(raw.Options)
	}
	if err != nil {
		return OutputSpec{}, err
	}
	if err := ValidateTarget(opts, target); err != nil {
		return OutputSpec{}, err
	}
	return OutputSpec{Name: raw.Name, Options: opts, Target: target}, nil
}

// ParseOutputs validates a multi-output list for size, naming, options, and target.
func ParseOutputs(raw []OutputInput) ([]OutputSpec, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: outputs cannot be empty", ErrInvalidOutputs)
	}
	if len(raw) > MaxOutputs {
		return nil, fmt.Errorf("%w: at most %d outputs per task", ErrTooManyOutputs, MaxOutputs)
	}
	specs := make([]OutputSpec, len(raw))
	for i, item := range raw {
		spec, err := ParseOutput(item)
		if err != nil {
			return nil, err
		}
		specs[i] = spec
	}
	if err := validateOutputUniqueness(specs); err != nil {
		return nil, err
	}
	return specs, nil
}

// ValidateWorkflowOutputs canonicalizes and validates already-decoded outputs
// stored or submitted on a workflow.
func ValidateWorkflowOutputs(outputs WorkflowOutputs) (WorkflowOutputs, error) {
	if len(outputs) == 0 {
		return nil, fmt.Errorf("%w: outputs cannot be empty", ErrInvalidOutputs)
	}
	if len(outputs) > MaxOutputs {
		return nil, fmt.Errorf("%w: at most %d outputs per workflow", ErrTooManyOutputs, MaxOutputs)
	}
	canonical := make(WorkflowOutputs, len(outputs))
	for i, out := range outputs {
		if out.Name != "" && !namePattern.MatchString(out.Name) {
			return nil, fmt.Errorf("%w: name %q must match [A-Za-z0-9_-]{1,64}", ErrInvalidOutputs, out.Name)
		}
		var (
			opts imageoptions.Options
			err  error
		)
		if out.Target != nil {
			opts, err = out.Options.CanonicalizeForTarget()
		} else {
			opts, err = out.Options.Canonicalize()
		}
		if err != nil {
			return nil, err
		}
		if err := ValidateTarget(opts, out.Target); err != nil {
			return nil, err
		}
		canonical[i] = OutputSpec{Name: out.Name, Options: opts, Target: out.Target}
	}
	if err := validateOutputUniqueness(canonical); err != nil {
		return nil, err
	}
	return canonical, nil
}

func validateOutputUniqueness(specs []OutputSpec) error {
	seenNames := make(map[string]struct{}, len(specs))
	seenFiles := make(map[string]struct{}, len(specs))
	multi := len(specs) > 1
	for i, spec := range specs {
		if spec.Name != "" {
			key := strings.ToLower(spec.Name)
			if _, exists := seenNames[key]; exists {
				return fmt.Errorf("%w: duplicate output name %q", ErrInvalidOutputs, spec.Name)
			}
			seenNames[key] = struct{}{}
		}
		if multi {
			label := spec.Name
			if label == "" {
				label = strconv.Itoa(i + 1)
			}
			fileKey := strings.ToLower(label + "." + Extension(spec.Options.Format))
			if _, exists := seenFiles[fileKey]; exists {
				return fmt.Errorf("%w: output %q collides with resolved filename suffix", ErrInvalidOutputs, label)
			}
			seenFiles[fileKey] = struct{}{}
		}
	}
	return nil
}
