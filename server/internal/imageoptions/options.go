// Package imageoptions mirrors compute-types' ImageOptions: the output intent
// of an image job. The rules are pinned by
// docs/contracts/fixtures/image-options.json, which the Rust side runs too.
package imageoptions

import (
	"bytes"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const MaxDimension = 16384

var ErrInvalid = errors.New("invalid image options")

type Format string

const (
	FormatAVIF Format = "avif"
	FormatJXL  Format = "jxl"
	FormatWebP Format = "webp"
	FormatJPEG Format = "jpeg"
	FormatPNG  Format = "png"
	FormatHEIC Format = "heic"
)

// defaultQuality is the quality used when a request leaves it out; 0 means
// the format has no quality setting.
var defaultQuality = map[Format]int{
	FormatAVIF: 60, FormatHEIC: 60, FormatJXL: 80, FormatWebP: 80, FormatJPEG: 82, FormatPNG: 0,
}

type Fit string

const (
	FitInside Fit = "inside"
	FitCover  Fit = "cover"
	FitFill   Fit = "fill"
)

type Color string

const (
	ColorKeep Color = "keep"
	ColorSRGB Color = "srgb"
)

type DynamicRange string

const (
	DynamicRangeKeep DynamicRange = "keep"
	DynamicRangeSDR  DynamicRange = "sdr"
)

type Metadata string

const (
	MetadataKeep  Metadata = "keep"
	MetadataStrip Metadata = "strip"
)

type Chroma string

const (
	Chroma420 Chroma = "420"
	Chroma444 Chroma = "444"
)

type Resize struct {
	Width   *int `json:"width,omitempty"`
	Height  *int `json:"height,omitempty"`
	Fit     Fit  `json:"fit"`
	Enlarge bool `json:"enlarge"`
}

// Options field order matches the Rust struct, so canonical JSON is identical.
type Options struct {
	Format       Format       `json:"format"`
	Quality      *int         `json:"quality,omitempty"`
	Lossless     bool         `json:"lossless"`
	Effort       *int         `json:"effort,omitempty"`
	Resize       *Resize      `json:"resize,omitempty"`
	Color        Color        `json:"color"`
	DynamicRange DynamicRange `json:"dynamic_range"`
	Metadata     Metadata     `json:"metadata"`
	Chroma       *Chroma      `json:"chroma,omitempty"`
	BitDepth     *int         `json:"bit_depth,omitempty"`
}

// Fields with a default must not be null; only optional fields may be.
var (
	requiredTop    = []string{"format", "lossless", "color", "dynamic_range", "metadata", "resize"}
	requiredResize = []string{"fit", "enlarge"}
)

func rejectNulls(raw []byte, fields []string, nested string, nestedFields []string) error {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil // the strict decode reports the real error
	}
	for _, f := range fields {
		if v, ok := obj[f]; ok && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return invalid(f + " cannot be null")
		}
	}
	if v, ok := obj[nested]; ok && nested != "" {
		return rejectNulls(v, nestedFields, "", nil)
	}
	return nil
}

// Parse decodes strictly (unknown fields and nulls for defaulted fields are
// errors, as in the Rust decoder) and canonicalizes.
func Parse(raw []byte) (Options, error) {
	if err := rejectNulls(raw, requiredTop, "resize", requiredResize); err != nil {
		return Options{}, err
	}
	var o Options
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&o); err != nil {
		return Options{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Options{}, fmt.Errorf("%w: unexpected trailing JSON", ErrInvalid)
	}
	return o.Canonicalize()
}

func invalid(msg string) error { return fmt.Errorf("%w: %s", ErrInvalid, msg) }

// Validate checks enum values and the cross-field rules.
func (o Options) Validate() error {
	if _, ok := defaultQuality[o.Format]; !ok {
		return invalid(fmt.Sprintf("unknown format %q", o.Format))
	}
	switch o.Color {
	case "", ColorKeep, ColorSRGB:
	default:
		return invalid(fmt.Sprintf("unknown color %q", o.Color))
	}
	switch o.DynamicRange {
	case "", DynamicRangeKeep, DynamicRangeSDR:
	default:
		return invalid(fmt.Sprintf("unknown dynamic_range %q", o.DynamicRange))
	}
	switch o.Metadata {
	case "", MetadataKeep, MetadataStrip:
	default:
		return invalid(fmt.Sprintf("unknown metadata %q", o.Metadata))
	}
	if o.Quality != nil {
		if *o.Quality < 1 || *o.Quality > 100 {
			return invalid("quality must be 1–100")
		}
		if o.Lossless {
			return invalid("quality cannot be combined with lossless")
		}
		if o.Format == FormatPNG {
			return invalid("png has no quality setting")
		}
	}
	if o.Lossless && o.Format == FormatJPEG {
		return invalid("jpeg cannot be lossless")
	}
	if o.Effort != nil {
		if *o.Effort < 1 || *o.Effort > 10 {
			return invalid("effort must be 1–10")
		}
		if o.Format == FormatJPEG {
			return invalid("jpeg has no effort setting")
		}
	}
	if r := o.Resize; r != nil {
		inRange := func(v *int) bool { return v == nil || (*v >= 1 && *v <= MaxDimension) }
		if r.Width == nil && r.Height == nil {
			return invalid("resize needs width or height")
		}
		if !inRange(r.Width) || !inRange(r.Height) {
			return invalid("resize width and height must be 1–16384")
		}
		switch r.Fit {
		case "", FitInside:
		case FitCover, FitFill:
			if r.Width == nil || r.Height == nil {
				return invalid("cover and fill need both width and height")
			}
		default:
			return invalid(fmt.Sprintf("unknown fit %q", r.Fit))
		}
	}
	if o.Chroma != nil {
		if *o.Chroma != Chroma420 && *o.Chroma != Chroma444 {
			return invalid(fmt.Sprintf("unknown chroma %q", *o.Chroma))
		}
		if o.Format != FormatAVIF && o.Format != FormatHEIC && o.Format != FormatJPEG {
			return invalid("chroma applies to avif, heic and jpeg only")
		}
		if o.Lossless {
			return invalid("chroma cannot be combined with lossless")
		}
	}
	if o.BitDepth != nil {
		if *o.BitDepth != 8 && *o.BitDepth != 10 {
			return invalid("bit_depth must be 8 or 10")
		}
		if o.Format != FormatAVIF && o.Format != FormatHEIC {
			return invalid("bit_depth applies to avif and heic only")
		}
	}
	return nil
}

// Canonicalize validates and fills every default, so equal intents serialize
// equally. The result is what gets stored and dispatched.
// Effort stays unset: its default is the encoder's own.
func (o Options) Canonicalize() (Options, error) {
	if err := o.Validate(); err != nil {
		return Options{}, err
	}
	if o.Quality == nil && !o.Lossless && defaultQuality[o.Format] != 0 {
		q := defaultQuality[o.Format]
		o.Quality = &q
	}
	if o.Resize != nil {
		r := *o.Resize
		if r.Fit == "" {
			r.Fit = FitInside
		}
		o.Resize = &r
	}
	if o.Color == "" {
		o.Color = ColorKeep
	}
	if o.DynamicRange == "" {
		o.DynamicRange = DynamicRangeKeep
	}
	if o.Metadata == "" {
		o.Metadata = MetadataStrip
	}
	return o, nil
}

// ArtifactKey identifies the output of applying canonical options to a
// source, so identical requests can reuse one artifact. `source` must identify
// the content itself (a file ID, or a verified SHA-256), never a client claim.
func ArtifactKey(source string, canonical Options) (string, error) {
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte("image-options/v2\n" + source + "\n" + string(encoded)))
	return hex.EncodeToString(sum[:]), nil
}

// Value stores options as jsonb.
func (o Options) Value() (driver.Value, error) {
	encoded, err := json.Marshal(o)
	return string(encoded), err
}

// Scan reads stored options; they were canonicalized before being written.
func (o *Options) Scan(value any) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, o)
	case string:
		return json.Unmarshal([]byte(v), o)
	default:
		return fmt.Errorf("scan image options: unsupported %T", value)
	}
}
