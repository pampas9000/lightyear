package operation

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"transcoder/server/internal/transcode"
)

type Kind string

const (
	Transcode Kind = "transcode"
	Generate  Kind = "image.generate"
	Upscale   Kind = "image.upscale"
	Edit      Kind = "image.edit"
)

var ErrInvalid = errors.New("invalid operation")

// JSON stores an immutable, validated operation snapshot without encoder coupling.
type JSON json.RawMessage

func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return json.RawMessage(j).MarshalJSON()
}

func (j *JSON) UnmarshalJSON(b []byte) error {
	if !json.Valid(b) {
		return errors.New("invalid JSON")
	}
	*j = bytes.Clone(b)
	return nil
}

func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	if !json.Valid(j) {
		return nil, errors.New("invalid JSON")
	}
	return string(j), nil
}

func (j *JSON) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*j = nil
	case string:
		return j.UnmarshalJSON([]byte(v))
	case []byte:
		return j.UnmarshalJSON(v)
	default:
		return fmt.Errorf("scan JSON: unsupported %T", value)
	}
	return nil
}

type GenerateParams struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Count  int    `json:"count"`
}

type UpscaleParams struct {
	Model string `json:"model"`
	Scale int    `json:"scale"`
}

type EditParams struct {
	Model    string   `json:"model"`
	Prompt   string   `json:"prompt"`
	Strength *float64 `json:"strength,omitempty"`
}

// Normalize returns canonical JSON so object key ordering does not affect idempotency.
func Normalize(kind Kind, raw JSON, targetFormat string) (JSON, int, error) {
	var value any
	count := 1
	switch kind {
	case Transcode:
		var p transcode.Params
		if len(raw) == 0 {
			raw = JSON("{}")
		}
		if err := decode(raw, &p); err != nil {
			return nil, 0, err
		}
		if err := p.Validate(targetFormat); err != nil {
			return nil, 0, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		value = p
	case Generate:
		var p GenerateParams
		if err := decode(raw, &p); err != nil {
			return nil, 0, err
		}
		p.Model, p.Prompt = strings.TrimSpace(p.Model), strings.TrimSpace(p.Prompt)
		if p.Count == 0 {
			p.Count = 1
		}
		if p.Model == "" || p.Prompt == "" || len(p.Prompt) > 32000 ||
			p.Width < 64 || p.Width > 8192 || p.Height < 64 || p.Height > 8192 ||
			p.Count < 1 || p.Count > 16 {
			return nil, 0, fmt.Errorf("%w: invalid generation parameters", ErrInvalid)
		}
		count, value = p.Count, p
	case Upscale:
		var p UpscaleParams
		if err := decode(raw, &p); err != nil {
			return nil, 0, err
		}
		p.Model = strings.TrimSpace(p.Model)
		if p.Model == "" || (p.Scale != 2 && p.Scale != 4 && p.Scale != 8) {
			return nil, 0, fmt.Errorf("%w: model and scale (2, 4 or 8) are required", ErrInvalid)
		}
		value = p
	case Edit:
		var p EditParams
		if err := decode(raw, &p); err != nil {
			return nil, 0, err
		}
		p.Model, p.Prompt = strings.TrimSpace(p.Model), strings.TrimSpace(p.Prompt)
		if p.Model == "" || p.Prompt == "" || len(p.Prompt) > 32000 ||
			(p.Strength != nil && (*p.Strength < 0 || *p.Strength > 1)) {
			return nil, 0, fmt.Errorf("%w: invalid editing parameters", ErrInvalid)
		}
		value = p
	default:
		return nil, 0, fmt.Errorf("%w: unsupported kind %q", ErrInvalid, kind)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var canonical any
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		return nil, 0, err
	}
	encoded, err = json.Marshal(canonical)
	return JSON(encoded), count, err
}

func decode(raw JSON, value any) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("%w: operation_params must be an object", ErrInvalid)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: unexpected trailing JSON", ErrInvalid)
	}
	return nil
}

func ValidateInputs(kind Kind, roles []string) error {
	source, mask := 0, 0
	for _, role := range roles {
		switch role {
		case "source":
			source++
		case "mask":
			mask++
		case "reference":
			if kind != Edit {
				return fmt.Errorf("%w: references are only supported for image.edit", ErrInvalid)
			}
		default:
			return fmt.Errorf("%w: unsupported input role %q", ErrInvalid, role)
		}
	}
	switch kind {
	case Generate:
		if len(roles) != 0 {
			return fmt.Errorf("%w: image.generate takes no input files", ErrInvalid)
		}
	case Transcode, Upscale:
		if len(roles) != 1 || source != 1 {
			return fmt.Errorf("%w: exactly one source is required", ErrInvalid)
		}
	case Edit:
		if source != 1 || mask > 1 || len(roles) > 16 {
			return fmt.Errorf("%w: one source, at most one mask and 14 references are supported", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported kind %q", ErrInvalid, kind)
	}
	return nil
}
