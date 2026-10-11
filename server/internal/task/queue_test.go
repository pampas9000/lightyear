package task

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSharedComputeFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/contracts/fixtures/compute-v1.0.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload ComputePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.AttemptScopedCancel || payload.OutputFileID == "" {
		t.Fatal("new compute fields were not decoded")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, raw, encoded)
}

func TestSharedResultFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/contracts/fixtures/result-v1.0.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload ResultPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Artifacts) != 1 || payload.Artifacts[0].FileID == "" || payload.Artifacts[0].Size != 1234 {
		t.Fatal("artifact contract was not decoded")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, raw, encoded)
}

func TestLegacyPayloadsOmitOptionalFields(t *testing.T) {
	var compute ComputePayload
	if err := json.Unmarshal([]byte(`{"schema_version":"1.0","job_id":"legacy","attempt_id":"legacy"}`), &compute); err != nil {
		t.Fatal(err)
	}
	if compute.AttemptScopedCancel || compute.OutputFileID != "" {
		t.Fatal("legacy cancellation defaults changed")
	}
	var result ResultPayload
	if err := json.Unmarshal([]byte(`{"schema_version":"1.0","status":"COMPLETED","metadata":{"output_size_bytes":123}}`), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts) != 0 {
		t.Fatal("legacy result unexpectedly requires artifacts")
	}
}

func assertJSONEqual(t *testing.T, expected, actual []byte) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(expected, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(actual, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("wire fixture mismatch:\nexpected: %s\nactual: %s", expected, actual)
	}
}
