package imageoptions

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type fixtureCase struct {
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Canonical json.RawMessage `json:"canonical"`
}

func TestSharedFixtureCases(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/contracts/fixtures/image-options.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []fixtureCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := Parse(c.Input)
			if c.Canonical == nil {
				if err == nil {
					t.Fatalf("expected rejection, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(got)
			var gotValue, wantValue any
			_ = json.Unmarshal(encoded, &gotValue)
			_ = json.Unmarshal(c.Canonical, &wantValue)
			if !reflect.DeepEqual(gotValue, wantValue) {
				t.Fatalf("canonical mismatch\n got: %s\nwant: %s", encoded, c.Canonical)
			}
		})
	}
}

func TestArtifactKeyIgnoresSpellingButNotIntentOrSource(t *testing.T) {
	key := func(source, raw string) string {
		t.Helper()
		o, err := Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		k, err := ArtifactKey(source, o)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	base := key("abc", `{"format":"avif"}`)
	if got := key("abc", `{"metadata":"strip","effort":5,"quality":60,"format":"avif"}`); got != base {
		t.Error("explicit defaults should produce the same key")
	}
	if key("abc", `{"format":"avif","quality":61}`) == base {
		t.Error("different quality must change the key")
	}
	if key("abd", `{"format":"avif"}`) == base {
		t.Error("different source must change the key")
	}
}
