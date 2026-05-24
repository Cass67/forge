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
	for _, want := range []ItemKind{ItemCheckpoint, ItemAgentHandoff, ItemTurnContract} {
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
	if _, ok := props["turn_contract"]; !ok {
		t.Fatalf("schema properties missing turn_contract: %#v", props)
	}
	toolResult := props["tool_result"].(JSONSchema)
	toolResultProps := toolResult["properties"].(map[string]any)
	for _, field := range []string{"handle", "sha256", "original_bytes"} {
		if _, ok := toolResultProps[field]; !ok {
			t.Fatalf("tool_result schema missing %s: %#v", field, toolResultProps)
		}
	}
}

func TestGeneratedSchemaConstrainsTurnContractFields(t *testing.T) {
	schema := GenerateProtocolSchema()
	props := schema["properties"].(JSONSchema)
	turnContract := props["turn_contract"].(JSONSchema)
	turnContractProps := turnContract["properties"].(map[string]any)

	assertSchemaRequired(t, turnContract, "id", "status")
	assertSchemaEnum(t, turnContractProps["intent"].(JSONSchema), "implement", "verify")
	assertSchemaEnum(t, turnContractProps["status"].(JSONSchema), "active", "satisfied", "cleared")

	requiredActions := turnContractProps["required_actions"].(JSONSchema)
	actionItems := requiredActions["items"].(JSONSchema)
	actionProps := actionItems["properties"].(map[string]any)
	assertSchemaRequired(t, actionItems, "kind")
	assertSchemaEnum(t, actionProps["kind"].(JSONSchema), "edit", "run", "report")

	evidence := turnContractProps["evidence"].(JSONSchema)
	evidenceItems := evidence["items"].(JSONSchema)
	evidenceProps := evidenceItems["properties"].(map[string]any)
	assertSchemaRequired(t, evidenceItems, "kind")
	assertSchemaEnum(t, evidenceProps["kind"].(JSONSchema), "test", "tool", "note", "read", "write", "verification", "delegation", "delegation_failure", "model_violation")

	gates := turnContractProps["gates"].(JSONSchema)
	gateItems := gates["items"].(JSONSchema)
	gateProps := gateItems["properties"].(map[string]any)
	assertSchemaRequired(t, gateItems, "name", "status")
	assertSchemaEnum(t, gateProps["status"].(JSONSchema), "pending", "passed", "failed")

	requiredArtifacts := turnContractProps["required_artifacts"].(JSONSchema)
	assertSchemaRequired(t, requiredArtifacts["items"].(JSONSchema), "path")
	requiredVerification := turnContractProps["required_verification"].(JSONSchema)
	assertSchemaRequired(t, requiredVerification["items"].(JSONSchema), "command")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertSchemaEnum(t *testing.T, schema JSONSchema, want ...string) {
	t.Helper()
	got, ok := schema["enum"].([]string)
	if !ok {
		t.Fatalf("schema %#v missing string enum", schema)
	}
	for _, value := range want {
		if !containsString(got, value) {
			t.Fatalf("enum %#v missing %q", got, value)
		}
	}
}

func assertSchemaRequired(t *testing.T, schema JSONSchema, want ...string) {
	t.Helper()
	got, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("schema %#v missing required fields", schema)
	}
	for _, field := range want {
		if !containsString(got, field) {
			t.Fatalf("required %#v missing %q", got, field)
		}
	}
}
