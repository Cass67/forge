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

func TestSanitizeToolSchemaAddsMissingTypes(t *testing.T) {
	schema := &llm.ToolSchema{
		Properties: map[string]*llm.ToolSchema{
			"password": {Description: "Optional password"},
			"filters":  {Properties: map[string]*llm.ToolSchema{}},
			"values":   {Items: &llm.ToolSchema{Type: "integer"}},
		},
	}
	out := SanitizeToolSchema(schema)
	if out.Type != "object" {
		t.Fatalf("root type = %q, want object", out.Type)
	}
	if got := out.Properties["password"].Type; got != "string" {
		t.Fatalf("password type = %q, want string", got)
	}
	if got := out.Properties["filters"].Type; got != "object" {
		t.Fatalf("filters type = %q, want object", got)
	}
	if got := out.Properties["values"].Type; got != "array" {
		t.Fatalf("values type = %q, want array", got)
	}
}
