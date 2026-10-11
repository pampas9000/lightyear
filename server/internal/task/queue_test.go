package task

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestDispatchFixtureRoundTrips(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/contracts/fixtures/dispatch-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload Dispatch
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.V != Version || payload.Options.Format != "avif" {
		t.Fatalf("unexpected dispatch: %+v", payload)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, raw, encoded)
}

func TestResultFixturesRoundTrip(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/contracts/fixtures/results-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		var result Result
		if err := json.Unmarshal(item, &result); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONEqual(t, item, encoded)
	}
}

func assertJSONEqual(t *testing.T, expected, actual []byte) {
	t.Helper()
	var want, got any
	if err := json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(actual, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("JSON mismatch\nwant: %s\n got: %s", expected, actual)
	}
}
