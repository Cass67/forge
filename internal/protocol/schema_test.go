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

func TestGeneratedSchemaIncludesDurableRuntimeItems(t *testing.T) {
	schema := GenerateProtocolSchema()
	props := schema["properties"].(JSONSchema)
	kind := props["kind"].(JSONSchema)
	for _, want := range []ItemKind{ItemCheckpoint, ItemAgentHandoff} {
		if !containsString(kind["enum"].([]string), string(want)) {
			t.Fatalf("kind enum missing %q: %#v", want, kind["enum"])
		}
	}
	if _, ok := props["checkpoint"]; !ok {
		t.Fatalf("schema properties missing checkpoint: %#v", props)
	}
	if _, ok := props["agent_handoff"]; !ok {
		t.Fatalf("schema properties missing agent_handoff: %#v", props)
	}
	toolResult := props["tool_result"].(JSONSchema)
	toolResultProps := toolResult["properties"].(map[string]any)
	for _, field := range []string{"handle", "sha256", "original_bytes"} {
		if _, ok := toolResultProps[field]; !ok {
			t.Fatalf("tool_result schema missing %s: %#v", field, toolResultProps)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
