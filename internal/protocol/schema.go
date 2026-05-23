package protocol

import "forge/internal/llm"

type JSONSchema map[string]any

func GenerateProtocolSchema() JSONSchema {
	return JSONSchema{
		"$id":                  "https://forge.local/schemas/forge_protocol.schema.json",
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"additionalProperties": false,
		"properties": JSONSchema{
			"at":        JSONSchema{"format": "date-time", "type": "string"},
			"id":        stringSchema(),
			"kind":      JSONSchema{"enum": []string{string(ItemSessionMeta), string(ItemTurnContext), string(ItemUserMessage), string(ItemAssistantMessage), string(ItemToolCall), string(ItemToolResult), string(ItemRetry), string(ItemFailure), string(ItemStats), string(ItemCompaction), string(ItemCheckpoint), string(ItemAgentHandoff), string(ItemSkillContext), string(ItemSideEffectIntent), string(ItemTurnComplete)}, "type": "string"},
			"seq":       JSONSchema{"type": "integer"},
			"thread_id": stringSchema(),
			"turn_id":   stringSchema(),
			"version":   JSONSchema{"const": CurrentItemVersion, "type": "integer"},

			"compaction":         objectSchema(map[string]any{"summary": stringSchema()}),
			"agent_handoff":      objectSchema(map[string]any{"agent_id": stringSchema(), "blocking": JSONSchema{"type": "boolean"}, "incidents": JSONSchema{"items": agentIncidentSchema(), "type": "array"}, "remaining_actions": JSONSchema{"items": agentActionSchema(), "type": "array"}}),
			"checkpoint":         objectSchema(map[string]any{"changed_files": JSONSchema{"items": stringSchema(), "type": "array"}, "error": stringSchema(), "id": stringSchema(), "phase": stringSchema()}),
			"failure":            objectSchema(map[string]any{"decision": FailureDecisionSchema()}),
			"message":            objectSchema(map[string]any{"role": stringSchema(), "text": stringSchema()}),
			"retry":              objectSchema(map[string]any{"attempt": JSONSchema{"type": "integer"}, "reason": stringSchema()}),
			"session_meta":       objectSchema(map[string]any{"cwd": stringSchema(), "model": stringSchema(), "source": stringSchema()}),
			"skill_context":      objectSchema(map[string]any{"body": stringSchema(), "name": stringSchema()}),
			"side_effect_intent": sideEffectIntentSchema(),
			"stats":              objectSchema(map[string]any{"duration_ms": JSONSchema{"type": "integer"}, "usage": JSONSchema{"type": "object"}}),
			"tool_call":          objectSchema(map[string]any{"args": JSONSchema{"type": "object"}, "tool_call_id": stringSchema(), "tool_name": stringSchema()}),
			"tool_result":        objectSchema(map[string]any{"diff": stringSchema(), "handle": stringSchema(), "is_error": JSONSchema{"type": "boolean"}, "original_bytes": JSONSchema{"type": "integer"}, "sha256": stringSchema(), "text": stringSchema(), "tool_call_id": stringSchema(), "tool_name": stringSchema(), "truncated": JSONSchema{"type": "boolean"}}),
			"turn_complete":      objectSchema(map[string]any{"response_id": stringSchema(), "status": JSONSchema{"enum": []string{string(TurnStatusCompleted), string(TurnStatusFailed), string(TurnStatusInterrupted)}, "type": "string"}}),
			"turn_context":       objectSchema(map[string]any{"input": stringSchema(), "mode": stringSchema(), "response_id": stringSchema()}),
		},
		"required": []string{"version", "id", "thread_id", "seq", "kind", "at"},
		"title":    "Forge Durable Runtime Protocol",
		"type":     "object",
	}
}

func FailureDecisionSchema() JSONSchema {
	return objectSchema(map[string]any{
		"class":        JSONSchema{"enum": []string{string(FailureNone), string(FailureModelOutputInvalid), string(FailureToolArgsInvalid), string(FailurePolicyBlocked), string(FailureToolRuntimeFailed), string(FailureProviderUnavailable), string(FailureUserCancelled)}, "type": "string"},
		"feedback":     stringSchema(),
		"recoverable":  JSONSchema{"type": "boolean"},
		"user_visible": JSONSchema{"type": "boolean"},
	})
}

func agentActionSchema() JSONSchema {
	return objectSchema(map[string]any{"blocking": JSONSchema{"type": "boolean"}, "description": stringSchema(), "kind": stringSchema(), "suggested_command": stringSchema(), "target_path": stringSchema()})
}

func agentIncidentSchema() JSONSchema {
	return objectSchema(map[string]any{"blocking": JSONSchema{"type": "boolean"}, "description": stringSchema(), "kind": stringSchema(), "paths": JSONSchema{"items": stringSchema(), "type": "array"}})
}

func sideEffectIntentSchema() JSONSchema {
	return objectSchema(map[string]any{
		"allowed_paths":    JSONSchema{"items": stringSchema(), "type": "array"},
		"artifact_paths":   JSONSchema{"items": stringSchema(), "type": "array"},
		"gates":            JSONSchema{"items": sideEffectGateSchema(), "type": "array"},
		"id":               stringSchema(),
		"incident_mode":    JSONSchema{"type": "boolean"},
		"reason":           stringSchema(),
		"remote":           stringSchema(),
		"required_actions": JSONSchema{"items": stringSchema(), "type": "array"},
		"source_turn":      JSONSchema{"type": "integer"},
		"target_branch":    stringSchema(),
		"workspace_root":   stringSchema(),
	})
}

func sideEffectGateSchema() JSONSchema {
	return objectSchema(map[string]any{"evidence": stringSchema(), "name": stringSchema(), "status": stringSchema()})
}

func ToolSchemaToJSONSchema(schema *llm.ToolSchema) JSONSchema {
	if schema == nil {
		return nil
	}
	out := JSONSchema{"type": schema.Type}
	if schema.Description != "" {
		out["description"] = schema.Description
	}
	if len(schema.Properties) > 0 {
		props := JSONSchema{}
		for name, prop := range schema.Properties {
			props[name] = ToolSchemaToJSONSchema(prop)
		}
		out["properties"] = props
	}
	if schema.Items != nil {
		out["items"] = ToolSchemaToJSONSchema(schema.Items)
	}
	if len(schema.Required) > 0 {
		out["required"] = schema.Required
	}
	if len(schema.Enum) > 0 {
		out["enum"] = schema.Enum
	}
	if schema.AdditionalProperties != nil {
		out["additionalProperties"] = *schema.AdditionalProperties
	}
	return out
}

func objectSchema(properties map[string]any) JSONSchema {
	return JSONSchema{"additionalProperties": false, "properties": properties, "type": "object"}
}

func stringSchema() JSONSchema {
	return JSONSchema{"type": "string"}
}
