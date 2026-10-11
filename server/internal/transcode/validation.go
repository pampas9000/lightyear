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

	if p.Color != nil {
		if err := validate.Struct(p.Color); err != nil {
			return fmt.Errorf("color encoding validation: %w", err)
		}
		if p.Color.NominalBitDepth != nil &&
			(*p.Color.NominalBitDepth != 8 && *p.Color.NominalBitDepth != 10) {
			return fmt.Errorf("color encoding validation: nominal_bit_depth supports only 8 or 10")
		}
	}

	switch format {
	case "AVIF":
		if p.Engine != "libavif:avif" {
			return fmt.Errorf("%w: expected 'libavif:avif' for AVIF, got %q", ErrUnsupportedEngine, p.Engine)
		}
		if len(p.EngineParams) == 0 {
			return validateAVIFColor(p.Color)
		}
		var avifOpts LibavifParams
		if err := json.Unmarshal(p.EngineParams, &avifOpts); err != nil {
			return fmt.Errorf("parse avif params: %w", err)
		}
		if err := validateAvif(avifOpts); err != nil {
			return err
		}
		if err := validateAVIFColor(p.Color); err != nil {
			return err
		}
		return nil

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

	case "HEIC":
		if p.Engine != "libheif:heic" {
			return fmt.Errorf("%w: expected 'libheif:heic' for HEIC, got %q", ErrUnsupportedEngine, p.Engine)
		}
		return nil

	default:
		// Skip validation for WebP, JPEG, PNG etc., allowing them to bypass check
		return nil
	}
}

func validateAvif(opts LibavifParams) error {
	if err := validate.Struct(opts); err != nil {
		return fmt.Errorf("libavif validation: %w", err)
	}
	if opts.Depth != nil && (*opts.Depth != 8 && *opts.Depth != 10) {
		return fmt.Errorf("libavif validation: depth supports only 8 or 10")
	}
	if opts.ColorRange != nil && *opts.ColorRange == ColorRangeLimited {
		return fmt.Errorf("libavif validation: limited range output is not supported")
	}
	return nil
}

func validateAVIFColor(color *ColorEncoding) error {
	if color == nil {
		return nil
	}
	if color.Chroma != nil && *color.Chroma == ChromaSubsamplingYuv400 {
		return fmt.Errorf("color encoding validation: AVIF yuv400 output is not supported")
	}
	if color.Range != nil && *color.Range == ColorRangeLimited {
		return fmt.Errorf("color encoding validation: AVIF limited range output is not supported")
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
