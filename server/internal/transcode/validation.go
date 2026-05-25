package transcode

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
)

var (
	ErrUnsupportedEngine        = errors.New("unsupported engine for the target format")
	ErrDistanceQualityExclusive = errors.New("distance and quality are mutually exclusive")
)

// Global validator instance
var validate = validator.New()

// Validate validates that the params are correct for the given target format (case-insensitive).
func (p *Params) Validate(targetFormat string) error {
	format := strings.ToUpper(strings.TrimSpace(targetFormat))

	switch format {
	case "AVIF":
		if p.Engine != "libavif:avif" {
			return fmt.Errorf("%w: expected 'libavif:avif' for AVIF, got %q", ErrUnsupportedEngine, p.Engine)
		}
		if len(p.EngineParams) == 0 {
			return nil
		}
		var avifOpts LibavifParams
		if err := json.Unmarshal(p.EngineParams, &avifOpts); err != nil {
			return fmt.Errorf("parse avif params: %w", err)
		}
		return validateAvif(avifOpts)

	case "JXL":
		if p.Engine != "libjxl:jxl" {
			return fmt.Errorf("%w: expected 'libjxl:jxl' for JXL, got %q", ErrUnsupportedEngine, p.Engine)
		}
		if len(p.EngineParams) == 0 {
			return nil
		}
		var jxlOpts LibjxlParams
		if err := json.Unmarshal(p.EngineParams, &jxlOpts); err != nil {
			return fmt.Errorf("parse jxl params: %w", err)
		}
		return validateJxl(jxlOpts)

	default:
		// Skip validation for WebP, JPEG, PNG etc., allowing them to bypass check
		return nil
	}
}

func validateAvif(opts LibavifParams) error {
	if err := validate.Struct(opts); err != nil {
		return fmt.Errorf("libavif validation: %w", err)
	}
	return nil
}

func validateJxl(opts LibjxlParams) error {
	if opts.Distance != nil && opts.Quality != nil {
		return ErrDistanceQualityExclusive
	}
	if err := validate.Struct(opts); err != nil {
		return fmt.Errorf("libjxl validation: %w", err)
	}
	return nil
}
