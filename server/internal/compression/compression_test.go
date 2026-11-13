package compression

import (
	"encoding/json"
	"errors"
	"testing"

	"transcoder/server/internal/imageoptions"
)

func TestParseOutputTargetAndSearchNeed(t *testing.T) {
	searched, err := ParseSingleOutput(
		json.RawMessage(`{"format":"avif"}`),
		json.RawMessage(`{"max_bytes":102400}`),
	)
	if err != nil {
		t.Fatalf("ParseSingleOutput searched: %v", err)
	}
	if searched.Options.Quality != nil {
		t.Fatalf("expected nil quality for target search, got %v", *searched.Options.Quality)
	}
	if !NeedsSearch(searched.Options, searched.Target) {
		t.Fatal("expected NeedsSearch to be true")
	}

	fixedQuality, err := ParseSingleOutput(
		json.RawMessage(`{"format":"avif","quality":80}`),
		json.RawMessage(`{"max_bytes":102400}`),
	)
	if err != nil {
		t.Fatalf("ParseSingleOutput fixed quality: %v", err)
	}
	if fixedQuality.Options.Quality == nil || *fixedQuality.Options.Quality != 80 {
		t.Fatalf("expected explicit quality 80 to survive, got %v", fixedQuality.Options.Quality)
	}
	if NeedsSearch(fixedQuality.Options, fixedQuality.Target) {
		t.Fatal("explicit quality should disable target search")
	}

	lossless, err := ParseSingleOutput(
		json.RawMessage(`{"format":"webp","lossless":true}`),
		json.RawMessage(`{"max_bytes":102400}`),
	)
	if err != nil {
		t.Fatalf("ParseSingleOutput lossless: %v", err)
	}
	if NeedsSearch(lossless.Options, lossless.Target) {
		t.Fatal("lossless should disable target search")
	}
}

func TestParseTargetValidationAndErrors(t *testing.T) {
	if got, err := ParseTarget(nil); err != nil || got != nil {
		t.Fatalf("expected nil, nil for empty raw, got %v, %v", got, err)
	}
	if got, err := ParseTarget(json.RawMessage(`null`)); err != nil || got != nil {
		t.Fatalf("expected nil, nil for null, got %v, %v", got, err)
	}

	invalidTargets := []string{`{}`, `{"max_bytes":null}`, `{"max_bytes":0}`, `{"max_bytes":-5}`, `{"unknown":1}`}
	for _, raw := range invalidTargets {
		if _, err := ParseTarget(json.RawMessage(raw)); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("ParseTarget(%s) expected ErrInvalidTarget, got %v", raw, err)
		}
	}

	if _, err := ParseSingleOutput(
		json.RawMessage(`{"format":"png"}`),
		json.RawMessage(`{"max_bytes":10000}`),
	); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("expected ErrInvalidTarget for png + target, got %v", err)
	}
}

func TestArtifactKeySeparatesSearchTargetsFromFixedOptions(t *testing.T) {
	searchOpts, _ := imageoptions.ParseForTarget([]byte(`{"format":"avif"}`))
	t1 := &Target{MaxBytes: new(int64(10000))}
	t2 := &Target{MaxBytes: new(int64(20000))}
	k1, err := ArtifactKey("src", searchOpts, t1)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := ArtifactKey("src", searchOpts, t2)
	if err != nil {
		t.Fatal(err)
	}
	if k1 == k2 {
		t.Fatal("different targets must produce distinct artifact keys")
	}

	fixedOpts, _ := imageoptions.ParseForTarget([]byte(`{"format":"avif","quality":60}`))
	plainKey, _ := imageoptions.ArtifactKey("src", fixedOpts)
	checkOnlyKey, _ := ArtifactKey("src", fixedOpts, t1)
	if plainKey != checkOnlyKey {
		t.Fatal("check-only target with explicit quality should share artifact key with identical fixed options")
	}
}

func TestParseOutputsNamingAndFilenameResolution(t *testing.T) {
	specs, err := ParseOutputs([]OutputInput{
		{Name: "preview", Options: json.RawMessage(`{"format":"avif"}`), Target: json.RawMessage(`{"max_bytes":50000}`)},
		{Options: json.RawMessage(`{"format":"jpeg","quality":85}`)},
	})
	if err != nil {
		t.Fatalf("ParseOutputs valid: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}

	if got := OutputFilename("photo.png", specs[0].Options.Format, specs[0].Name, 0, true); got != "photo.preview.avif" {
		t.Fatalf("got %q, want photo.preview.avif", got)
	}
	if got := OutputFilename("photo.png", specs[1].Options.Format, specs[1].Name, 1, true); got != "photo.2.jpg" {
		t.Fatalf("got %q, want photo.2.jpg", got)
	}
	if got := OutputFilename("photo.png", specs[0].Options.Format, specs[0].Name, 0, false); got != "photo.avif" {
		t.Fatalf("single-output filename got %q, want photo.avif", got)
	}

	// Duplicate names (case-insensitive)
	if _, err := ParseOutputs([]OutputInput{
		{Name: "Thumb", Options: json.RawMessage(`{"format":"avif"}`)},
		{Name: "thumb", Options: json.RawMessage(`{"format":"webp"}`)},
	}); !errors.Is(err, ErrInvalidOutputs) {
		t.Fatalf("expected ErrInvalidOutputs for duplicate name, got %v", err)
	}

	// Numeric name colliding with unnamed ordinal suffix
	if _, err := ParseOutputs([]OutputInput{
		{Name: "2", Options: json.RawMessage(`{"format":"avif"}`)},
		{Options: json.RawMessage(`{"format":"avif"}`)},
	}); !errors.Is(err, ErrInvalidOutputs) {
		t.Fatalf("expected ErrInvalidOutputs for ordinal suffix collision, got %v", err)
	}

	// Invalid characters in name
	if _, err := ParseOutputs([]OutputInput{
		{Name: "bad name", Options: json.RawMessage(`{"format":"avif"}`)},
	}); !errors.Is(err, ErrInvalidOutputs) {
		t.Fatalf("expected ErrInvalidOutputs for invalid name characters, got %v", err)
	}
}

func TestBuildStatsAndIsTargetMet(t *testing.T) {
	if s := BuildStats(0, 100); s != nil {
		t.Fatalf("expected nil stats for zero source bytes, got %+v", s)
	}
	smaller := BuildStats(1000, 400)
	if smaller == nil || smaller.Ratio != 0.4 || smaller.SavedRatio != 0.6 || smaller.LargerThanSource {
		t.Fatalf("unexpected stats for smaller output: %+v", smaller)
	}
	larger := BuildStats(100, 125)
	if larger == nil || larger.Ratio != 1.25 || !larger.LargerThanSource {
		t.Fatalf("unexpected stats for larger output: %+v", larger)
	}
	tMet := IsTargetMet(400, &Target{MaxBytes: new(int64(500))})
	if tMet == nil || !*tMet {
		t.Fatalf("expected target_met true")
	}
	tMissed := IsTargetMet(501, &Target{MaxBytes: new(int64(500))})
	if tMissed == nil || *tMissed {
		t.Fatalf("expected target_met false")
	}
}
