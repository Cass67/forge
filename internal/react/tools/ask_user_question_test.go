package reacttools

import (
	"context"
	"strings"
	"testing"
)

func TestAskUserQuestionFormatsStructuredQuestion(t *testing.T) {
	tool := NewAskUserQuestion()
	result, err := tool.Execute(context.Background(), map[string]any{
		"question": "Which implementation path should we take?",
		"options": []any{
			map[string]any{"label": "Prompt composer first", "description": "Build composition before more tools"},
			map[string]any{"label": "Plan mode first", "description": "Prioritize explicit workflows"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Question: Which implementation path should we take?",
		"1. Prompt composer first",
		"2. Plan mode first",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("result missing %q:\n%s", want, result)
		}
	}
}

func TestAskUserQuestionFormatsTypedOptions(t *testing.T) {
	tool := NewAskUserQuestion()
	result, err := tool.Execute(context.Background(), map[string]any{
		"question": "What area should we improve?",
		"options": []any{
			map[string]any{"label": "Testing", "description": "Strengthen coverage"},
			map[string]any{"label": "Docs", "description": "Improve onboarding"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Question: What area should we improve?",
		"1. Testing",
		"2. Docs",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("result missing %q:\n%s", want, result)
		}
	}
}

func TestAskUserQuestionUsesTypedOptionsSchema(t *testing.T) {
	tool := NewAskUserQuestion()
	if tool.Schema == nil {
		t.Fatal("expected typed schema")
	}
	if _, ok := tool.Schema.Properties["options_json"]; ok {
		t.Fatal("options_json should not be part of the native schema")
	}
	options := tool.Schema.Properties["options"]
	if options == nil || options.Type != "array" || options.Items == nil || options.Items.Type != "object" {
		t.Fatalf("options schema = %#v", options)
	}
	if !hasRequired(tool.Schema.Required, "question") || !hasRequired(tool.Schema.Required, "options") {
		t.Fatalf("required fields = %#v", tool.Schema.Required)
	}
	if options.Items.Properties["label"].Type != "string" || options.Items.Properties["description"].Type != "string" {
		t.Fatalf("option item schema = %#v", options.Items.Properties)
	}
}

func hasRequired(required []string, name string) bool {
	for _, item := range required {
		if item == name {
			return true
		}
	}
	return false
}
