package transcode

import (
	"encoding/json"
	"testing"
)

func ptr[T any](v T) *T {
	return &v
}

func TestParamsValidation_ColorEncoding(t *testing.T) {
	tests := []struct {
		name      string
		params    Params
		targetFmt string
		wantErr   bool
	}{
		{
			name: "valid color encoding",
			params: Params{
				Engine: "libavif:avif",
				Color: &ColorEncoding{
					Primaries:       ptr(ColorPrimariesDisplayP3),
					Transfer:        ptr(TransferCharacteristicsSrgb),
					Matrix:          ptr(MatrixCoefficientsBt601),
					Range:           ptr(ColorRangeFull),
					NominalBitDepth: ptr(10),
					Chroma:          ptr(ChromaSubsamplingAuto),
					ToneMapping:     ptr(ToneMappingOperatorAcesFilmic),
					Dither:          ptr(true),
				},
			},
			targetFmt: "AVIF",
			wantErr:   false,
		},
		{
			name: "valid coupled color space without legacy fields",
			params: Params{
				Engine: "libavif:avif",
				Color: &ColorEncoding{
					ColorSpace:      ptr(ColorSpaceBt2020Hlg),
					NominalBitDepth: ptr(10),
					Chroma:          ptr(ChromaSubsamplingAuto),
					Dither:          ptr(true),
				},
			},
			targetFmt: "AVIF",
			wantErr:   false,
		},
		{
			name: "invalid color space",
			params: Params{
				Engine: "libavif:avif",
				Color: &ColorEncoding{
					ColorSpace: ptr(ColorSpace("rec2100_dolby")),
				},
			},
			targetFmt: "AVIF",
			wantErr:   true,
		},
		{
			name: "invalid primaries",
			params: Params{
				Engine: "libavif:avif",
				Color: &ColorEncoding{
					Primaries: ptr(ColorPrimaries("unknown_prim")),
				},
			},
			targetFmt: "AVIF",
			wantErr:   true,
		},
		{
			name: "invalid tone mapping",
			params: Params{
				Engine: "libavif:avif",
				Color: &ColorEncoding{
					ToneMapping: ptr(ToneMappingOperator("magic_tonemap")),
				},
			},
			targetFmt: "AVIF",
			wantErr:   true,
		},
		{
			name: "unsupported 12 bit output",
			params: Params{
				Engine: "libavif:avif",
				Color: &ColorEncoding{
					NominalBitDepth: ptr(12),
				},
			},
			targetFmt: "AVIF",
			wantErr:   true,
		},
		{
			name: "limited range avif is rejected",
			params: Params{
				Engine: "libavif:avif",
				Color: &ColorEncoding{
					Range: ptr(ColorRangeLimited),
				},
			},
			targetFmt: "AVIF",
			wantErr:   true,
		},
		{
			name: "monochrome avif is rejected",
			params: Params{
				Engine: "libavif:avif",
				Color: &ColorEncoding{
					Chroma: ptr(ChromaSubsamplingYuv400),
				},
			},
			targetFmt: "AVIF",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.Validate(tt.targetFmt)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLibavifValidation_RejectsUnsupportedDepthAndRange(t *testing.T) {
	depth := 12
	params := Params{
		Engine:       "libavif:avif",
		EngineParams: mustJSON(LibavifParams{Depth: &depth}),
	}
	if err := params.Validate("AVIF"); err == nil {
		t.Fatal("expected 12-bit AVIF to be rejected")
	}

	limited := ColorRangeLimited
	params.EngineParams = mustJSON(LibavifParams{ColorRange: &limited})
	if err := params.Validate("AVIF"); err == nil {
		t.Fatal("expected limited-range AVIF to be rejected")
	}
}

func mustJSON[T any](value T) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestLibavifParams_NewFields(t *testing.T) {
	raw, err := json.Marshal(LibavifParams{
		Quality:    ptr(80),
		Depth:      ptr(10),
		ColorRange: ptr(ColorRangeFull),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	params := Params{
		Engine:       "libavif:avif",
		EngineParams: raw,
	}

	if err := params.Validate("AVIF"); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestParamsValuePreservesNonEngineFields(t *testing.T) {
	params := Params{
		Color: &ColorEncoding{
			ColorSpace: new(ColorSpaceDisplayP3),
		},
		Strict: new(true),
	}

	value, err := params.Value()
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if value == nil {
		t.Fatal("expected non-empty params to be persisted")
	}

	var decoded Params
	if err := decoded.Scan(value); err != nil {
		t.Fatalf("scan params: %v", err)
	}
	if decoded.Color == nil || decoded.Color.ColorSpace == nil || *decoded.Color.ColorSpace != ColorSpaceDisplayP3 {
		t.Fatalf("color settings were lost: %+v", decoded)
	}
	if decoded.Strict == nil || !*decoded.Strict {
		t.Fatalf("strict setting was lost: %+v", decoded)
	}
}
