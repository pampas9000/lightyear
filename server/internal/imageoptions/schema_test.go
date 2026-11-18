package imageoptions

import (
	"bytes"
	"os"
	"testing"
)

func TestJSONSchemaIsCurrent(t *testing.T) {
	want, err := os.ReadFile("../../../docs/contracts/schemas/image-options.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(JSONSchema, want) {
		t.Fatal("server/internal/imageoptions/image-options.schema.json is stale; run: task gen:types")
	}
}
