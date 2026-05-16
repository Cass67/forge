package protocol

import (
	"testing"

	"forge/internal/llm"
)

func TestSanitizeToolSchemaDefaultsObjectAdditionalPropertiesFalse(t *testing.T) {
	schema := &llm.ToolSchema{Type: "object", Properties: map[string]*llm.ToolSchema{"path": {Type: "string"}}}
	out := SanitizeToolSchema(schema)
	if out.AdditionalProperties == nil || *out.AdditionalProperties != false {
		t.Fatalf("additionalProperties = %#v, want false", out.AdditionalProperties)
	}
}

func TestSanitizeToolSchemaAddsArrayItemsObject(t *testing.T) {
	schema := &llm.ToolSchema{Type: "array"}
	out := SanitizeToolSchema(schema)
	if out.Items == nil || out.Items.Type != "object" {
		t.Fatalf("items = %#v, want object fallback", out.Items)
	}
}
