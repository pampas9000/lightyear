package transcode

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// Params represents the unified dynamic configuration format for the transcoder.
type Params struct {
	Engine       string          `json:"engine"`
	EngineParams json.RawMessage `json:"engine_params"`
}

// Scan implements the sql.Scanner interface for GORM database deserialization.
func (tp *Params) Scan(value any) error {
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	case nil:
		*tp = Params{}
		return nil
	default:
		return fmt.Errorf("scan Params: failed to convert %T to []byte", value)
	}
	if len(bytes) == 0 {
		*tp = Params{}
		return nil
	}
	return json.Unmarshal(bytes, tp)
}

// Value implements the driver.Valuer interface for GORM database serialization.
func (tp Params) Value() (driver.Value, error) {
	if tp.Engine == "" && len(tp.EngineParams) == 0 {
		return nil, nil
	}
	bytes, err := json.Marshal(tp)
	if err != nil {
		return nil, err
	}
	return string(bytes), nil
}

// LibavifParams represents the configuration options for the libavif encoder (avifenc).
type LibavifParams struct {
	// Quality sets the quality for color (0-100, where 100 is lossless). Maps to -q or --qcolor.
	Quality *int `json:"quality,omitempty" validate:"omitempty,min=0,max=100"`

	// AlphaQuality sets the quality for the alpha channel (0-100, where 100 is lossless). Maps to --qalpha.
	AlphaQuality *int `json:"alpha_quality,omitempty" validate:"omitempty,min=0,max=100"`

	// Speed controls encoding speed (0-10, where 0 is slowest/best compression and 10 is fastest).
	// Default is 6. Maps to -s or --speed.
	Speed *int `json:"speed,omitempty" validate:"omitempty,min=0,max=10"`

	// Jobs specifies the number of threads to use. Default is "all". Maps to -j or --jobs.
	// Manual limits (e.g., 2 or 4) are highly recommended in high-concurrency environments to reduce CPU context switching.
	Jobs *int `json:"jobs,omitempty" validate:"omitempty,min=1"`

	// SharpYUV enables sharp RGB-to-YUV conversion. Maps to --sharpyuv.
	// Highly recommended when converting RGB inputs (PNG/JPG) to YUV420 to reduce color bleeding on text/sharp edges.
	SharpYUV *bool `json:"sharp_yuv,omitempty"`

	// YUV specifies the output chroma subsampling format ("444", "420", "422", "auto").
	// Default is "auto". Maps to -y or --yuv.
	YUV *string `json:"yuv,omitempty" validate:"omitempty,oneof=444 420 422 auto"`

	// Advanced holds optional underlying aom-specific tuning parameters. Maps to -a or --advanced.
	Advanced *LibavifAdvancedParams `json:"advanced,omitempty" validate:"omitempty"`
}

// LibavifAdvancedParams represents advanced/tweak configurations for the libavif encoder.
// Color parameters map to aom-specific "c:parameter" options, while alpha parameters map to "a:parameter".
type LibavifAdvancedParams struct {
	// Sharpness specifies the sharpness of the transform blocks (0-7, default 0). Maps to aom-specific sharpness (c:sharpness).
	// Higher values (e.g., 7) reduce blur on anime/line-art but may introduce noise on real photos.
	Sharpness *int `json:"sharpness,omitempty" validate:"omitempty,min=0,max=7"`

	// ColorSharpness specifies the sharpness parameter specifically for the color channel.
	ColorSharpness *int `json:"color_sharpness,omitempty" validate:"omitempty,min=0,max=7"`

	// AlphaSharpness specifies the sharpness parameter specifically for the alpha channel.
	AlphaSharpness *int `json:"alpha_sharpness,omitempty" validate:"omitempty,min=0,max=7"`
}

// LibjxlParams represents the configuration options for the JPEG XL encoder (cjxl).
type LibjxlParams struct {
	// Distance sets the maximum visual error (0.0-15.0). Maps to -d or --distance.
	// 0.0 is mathematically lossless. 1.0 is visually lossless (default for non-JPEG inputs). Lower values mean higher quality.
	// Note: Distance and Quality are mutually exclusive.
	Distance *float64 `json:"distance,omitempty" validate:"omitempty,min=0,max=15"`

	// Quality sets the target quality (0-100, where 100 is lossless). Maps to -q or --quality.
	// Note: Quality and Distance are mutually exclusive.
	Quality *int `json:"quality,omitempty" validate:"omitempty,min=0,max=100"`

	// Effort controls encoder effort/complexity (1-10, default 7). Maps to -e or --effort.
	// Higher values improve compression ratio at the cost of encoding speed. Effort 5-6 is recommended for high concurrency servers.
	Effort *int `json:"effort,omitempty" validate:"omitempty,min=1,max=10"`

	// Progressive enables progressive/sequential rendering. Maps to --progressive.
	Progressive *bool `json:"progressive,omitempty"`
}


