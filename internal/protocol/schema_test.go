package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProtocolSchemaFixtureMatchesGenerated(t *testing.T) {
	generated, err := json.MarshalIndent(GenerateProtocolSchema(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(filepath.Join("schemas", "forge_protocol.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(expected) != string(generated)+"\n" {
		t.Fatalf("protocol schema fixture differs; regenerate internal/protocol/schemas/forge_protocol.schema.json\n%s", generated)
	}
}

func TestGeneratedSchemasHaveStableEnvelopeFields(t *testing.T) {
	schema := GenerateProtocolSchema()
	props := schema["properties"].(JSONSchema)
	for _, field := range []string{"version", "id", "thread_id", "seq", "kind", "at"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("missing stable envelope field %s in %#v", field, props)
		}
	}
}
