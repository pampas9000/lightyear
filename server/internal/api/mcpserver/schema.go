package mcpserver

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	v1 "transcoder/server/internal/api/v1"
	"transcoder/server/internal/compression"
	"transcoder/server/internal/imageoptions"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
)

// The tool schemas embed the ImageOptions schema generated from
// compute-types, so options are described once. Its definitions are hoisted
// to the root $defs of each tool schema, where its own "#/$defs/…"
// references resolve, and ImageOptions itself becomes $defs.ImageOptions.

const imageOptionsRef = "#/$defs/ImageOptions"

// imageOptionsDefs returns the $defs to put at the root of a schema that
// references ImageOptions.
func imageOptionsDefs() (map[string]*jsonschema.Schema, error) {
	var root jsonschema.Schema
	if err := json.Unmarshal(imageoptions.JSONSchema, &root); err != nil {
		return nil, fmt.Errorf("parse ImageOptions schema: %w", err)
	}
	defs := root.Defs
	if defs == nil {
		defs = map[string]*jsonschema.Schema{}
	}
	if _, ok := defs["ImageOptions"]; ok {
		return nil, fmt.Errorf("ImageOptions schema already defines $defs.ImageOptions")
	}
	root.Schema, root.Defs = "", nil
	defs["ImageOptions"] = &root
	for _, d := range defs {
		walk(d, dropRustFormats)
	}
	return defs, nil
}

// dropNull turns every nullable type, such as ["integer", "null"], into its
// single non-null type. Input schemas use it: an absent option means the same
// as null, and clients that map tool schemas onto a single-type dialect
// (Gemini function declarations) reject type arrays. Output schemas keep
// null, because results contain it.
func dropNull(s *jsonschema.Schema) {
	if len(s.Types) == 2 && slices.Contains(s.Types, "null") {
		for _, t := range s.Types {
			if t != "null" {
				s.Type = t
			}
		}
		s.Types = nil
	}
}

// dropRustFormats removes the formats schemars derives from Rust integer
// types (uint8, uint32). They are not JSON Schema formats: validators ignore
// them at best and warn or refuse the schema at worst. The ranges they hint
// at are in minimum and maximum where they matter.
func dropRustFormats(s *jsonschema.Schema) {
	if strings.HasPrefix(s.Format, "uint") || strings.HasPrefix(s.Format, "int") {
		s.Format = ""
	}
}

// walk calls fn on s and every schema inside it.
func walk(s *jsonschema.Schema, fn func(*jsonschema.Schema)) {
	if s == nil {
		return
	}
	fn(s)
	for _, sub := range s.Properties {
		walk(sub, fn)
	}
	for _, sub := range s.Defs {
		walk(sub, fn)
	}
	for _, list := range [][]*jsonschema.Schema{s.AllOf, s.AnyOf, s.OneOf, s.PrefixItems} {
		for _, sub := range list {
			walk(sub, fn)
		}
	}
	walk(s.Items, fn)
	walk(s.AdditionalProperties, fn)
	walk(s.Not, fn)
}

func ref(r string) *jsonschema.Schema { return &jsonschema.Schema{Ref: r} }

// closed is additionalProperties: false. Each use needs its own value: a schema
// must be a tree, so one pointer cannot appear twice.
func closed() *jsonschema.Schema { return &jsonschema.Schema{Not: &jsonschema.Schema{}} }

// typeSchemas maps Go types whose inferred schema would be wrong or vague.
var typeSchemas = map[reflect.Type]*jsonschema.Schema{
	reflect.TypeFor[uuid.UUID](): {Type: "string", Format: "uuid"},
	// Type is set so that the nullable form inferred for *Options becomes
	// ["null", "object"] next to the $ref, not ["null"] alone.
	reflect.TypeFor[imageoptions.Options](): {Type: "object", Ref: imageOptionsRef},
}

// outputSchema infers the schema of a /v1 representation and attaches the
// ImageOptions definitions.
func outputSchema[T any]() (*jsonschema.Schema, error) {
	s, err := jsonschema.For[T](&jsonschema.ForOptions{TypeSchemas: typeSchemas})
	if err != nil {
		return nil, err
	}
	defs, err := imageOptionsDefs()
	if err != nil {
		return nil, err
	}
	s.Defs = defs
	return s, nil
}

func targetSchema() (*jsonschema.Schema, error) {
	s, err := jsonschema.For[compression.Target](nil)
	if err != nil {
		return nil, err
	}
	s.Description = "Compression target: search for the smallest encoding that meets it. " +
		"max_bytes is the largest acceptable output size in bytes. format must not be png. " +
		"If options also give quality, distance or lossless, nothing is searched; the result is only checked against the target."
	if p := s.Properties["max_bytes"]; p != nil {
		min := 1.0
		p.Minimum = &min
		p.Types, p.Type = nil, "integer"
	}
	s.AdditionalProperties = closed()
	return s, nil
}

func createUploadInputSchema() *jsonschema.Schema {
	minSize := 1.0
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"filename": {Type: "string", MinLength: ptr(1), MaxLength: ptr(255),
				Description: "File name. Outputs are named after it (photo.jpg converted to AVIF becomes photo.avif)."},
			"size": {Type: "integer", Minimum: &minSize,
				Description: "Exact size of the file in bytes."},
			"content_type": {Type: "string",
				Description: "MIME type, such as image/jpeg. Defaults to application/octet-stream; the format is detected from the bytes either way."},
			"sha256": {Type: "string", Pattern: "^[0-9a-fA-F]{64}$",
				Description: "Hex SHA-256 of the file. Recommended: storage then checks the bytes, and a file you uploaded before is reused without uploading again (method none)."},
		},
		Required:             []string{"filename", "size"},
		AdditionalProperties: closed(),
	}
}

func submitTaskInputSchema() (*jsonschema.Schema, error) {
	defs, err := imageOptionsDefs()
	if err != nil {
		return nil, err
	}
	target, err := targetSchema()
	if err != nil {
		return nil, err
	}
	defs["Target"] = target
	for _, d := range defs {
		walk(d, dropNull)
	}
	item := &jsonschema.Schema{
		Type: "object",
		Description: "One source image: a file_id from create_upload, or a public http(s) url that the server downloads. " +
			"Give exactly one.",
		Properties: map[string]*jsonschema.Schema{
			"file_id": {Type: "string", Format: "uuid", Description: "file.id returned by create_upload."},
			"url":     {Type: "string", MaxLength: ptr(2048), Description: "http or https URL of the image. Private and internal addresses are refused."},
		},
		MinProperties:        ptr(1),
		MaxProperties:        ptr(1),
		AdditionalProperties: closed(),
	}
	output := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name":    {Type: "string", Description: "Optional label for this output, echoed as output_name on its jobs."},
			"options": ref(imageOptionsRef),
			"target":  ref("#/$defs/Target"),
		},
		Required:             []string{"options"},
		AdditionalProperties: closed(),
	}
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"items": {Type: "array", Items: item, MinItems: ptr(1), MaxItems: ptr(compression.MaxItems),
				Description: "Source images. Every source is converted to every output; each pair is one job."},
			"options": {Ref: imageOptionsRef,
				Description: "One output for every source. Only format is required. Give options (with an optional target) or outputs, not both."},
			"target": {Ref: "#/$defs/Target", Description: "Compression target for options. Not allowed with outputs; put a target on each output instead."},
			"outputs": {Type: "array", Items: output, MinItems: ptr(1), MaxItems: ptr(compression.MaxOutputs),
				Description: "Several outputs for every source, each with its own options and optional target."},
			"idempotency_key": {Type: "string", MinLength: ptr(1), MaxLength: ptr(255),
				Description: "Optional. Submitting again with the same key and the same request returns the task created the first time instead of a new one; a different request with the same key is an error. Use it when retrying a submit_task call whose result you did not receive."},
		},
		Required:             []string{"items"},
		AdditionalProperties: closed(),
		Defs:                 defs,
	}, nil
}

func getTaskInputSchema() *jsonschema.Schema {
	zero, maxWait := 0.0, float64(MaxWaitSeconds)
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"task_id": {Type: "string", Format: "uuid", Description: "id returned by submit_task."},
			"wait_seconds": {Type: "integer", Minimum: &zero, Maximum: &maxWait, Default: json.RawMessage("0"),
				Description: fmt.Sprintf("Wait up to this many seconds (at most %d) for the task to finish before answering. 0 answers at once.", MaxWaitSeconds)},
		},
		Required:             []string{"task_id"},
		AdditionalProperties: closed(),
	}
}

// Shapes of the structured results; they are the /v1 representations.
type (
	uploadOutput = v1.Upload
	taskOutput   = v1.Task
)

func ptr[T any](v T) *T { return &v }
