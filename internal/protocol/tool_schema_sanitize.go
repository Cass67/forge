package protocol

import "forge/internal/llm"

func SanitizeToolSchema(schema *llm.ToolSchema) *llm.ToolSchema {
	if schema == nil {
		return nil
	}
	out := *schema
	if out.Type == "" {
		switch {
		case schema.Properties != nil:
			out.Type = "object"
		case schema.Items != nil:
			out.Type = "array"
		default:
			out.Type = "string"
		}
	}
	if schema.Properties != nil {
		out.Properties = map[string]*llm.ToolSchema{}
		for name, prop := range schema.Properties {
			out.Properties[name] = SanitizeToolSchema(prop)
		}
	}
	if schema.Items != nil {
		out.Items = SanitizeToolSchema(schema.Items)
	}
	if out.Type == "object" && out.AdditionalProperties == nil {
		additional := false
		out.AdditionalProperties = &additional
	}
	if out.Type == "array" && out.Items == nil {
		out.Items = &llm.ToolSchema{Type: "object"}
	}
	return &out
}
