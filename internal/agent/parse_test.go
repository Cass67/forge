package agent

import (
	"testing"
)

func TestParseToolCalls(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{
			name:  "single tool call",
			input: "Let me read the file.\n\n<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"main.go\"}}\n</tool_call>",
			want:  1,
		},
		{
			name:  "multiple tool calls",
			input: "<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"a.go\"}}\n</tool_call>\n\nSome reasoning.\n\n<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"b.go\"}}\n</tool_call>",
			want:  2,
		},
		{
			name:  "no tool calls",
			input: "Just some text with no tools.",
			want:  0,
		},
		{
			name:  "tool call inside code fence ignored",
			input: "Here is an example:\n```\n<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"x.go\"}}\n</tool_call>\n```\n",
			want:  0,
		},
		{
			name:  "real call after code fence example",
			input: "Example:\n```\n<tool_call>\n{\"name\": \"fake\"}\n</tool_call>\n```\n\n<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"real.go\"}}\n</tool_call>",
			want:  1,
		},
		{
			name:  "function calls wrapper with array",
			input: "Thinking...\n<function_calls>\n[{\"name\": \"read_file\", \"args\": {\"path\": \"main.go\"}}, {\"name\": \"list_dir\", \"args\": {}}]\n</function_calls>",
			want:  2,
		},
		{
			name:  "function calls inside code fence ignored",
			input: "```xml\n<function_calls>\n[{\"name\": \"read_file\", \"args\": {\"path\": \"main.go\"}}]\n</function_calls>\n```",
			want:  0,
		},
		{
			name:  "invoke wrapper with single call",
			input: "Let me inspect that.\n<invoke>\n{\"name\": \"list_dir\", \"args\": {}}\n</invoke>",
			want:  1,
		},
		{
			name:  "invoke wrapper inside code fence ignored",
			input: "```xml\n<invoke>\n{\"name\": \"list_dir\", \"args\": {}}\n</invoke>\n```",
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, _ := ParseToolCalls(tt.input)
			if len(calls) != tt.want {
				t.Errorf("got %d tool calls, want %d", len(calls), tt.want)
			}
			if tt.want > 0 && calls[0].Name == "" {
				t.Error("tool call name is empty")
			}
		})
	}
}

func TestParseToolCallArgs(t *testing.T) {
	input := "<tool_call>\n{\"name\": \"edit_file\", \"args\": {\"path\": \"main.go\", \"old_text\": \"foo\", \"new_text\": \"bar\"}}\n</tool_call>"
	calls, _ := ParseToolCalls(input)
	if len(calls) != 1 {
		t.Fatal("expected 1 call")
	}
	if calls[0].Name != "edit_file" {
		t.Errorf("name = %q", calls[0].Name)
	}
	if calls[0].Args["path"] != "main.go" {
		t.Errorf("path = %v", calls[0].Args["path"])
	}
	if calls[0].Args["old_text"] != "foo" {
		t.Errorf("old_text = %v", calls[0].Args["old_text"])
	}
}

func TestParseFunctionCallsArgs(t *testing.T) {
	input := "<function_calls>\n[{\"name\": \"edit_file\", \"args\": {\"path\": \"main.go\", \"old_text\": \"foo\", \"new_text\": \"bar\"}}]\n</function_calls>"
	calls, visible := ParseToolCalls(input)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if visible != "" {
		t.Fatalf("expected no visible text, got %q", visible)
	}
	if calls[0].Name != "edit_file" {
		t.Errorf("name = %q", calls[0].Name)
	}
	if calls[0].Args["path"] != "main.go" {
		t.Errorf("path = %v", calls[0].Args["path"])
	}
}

func TestParseInvokeArgs(t *testing.T) {
	input := "<invoke>\n{\"name\": \"edit_file\", \"args\": {\"path\": \"main.go\", \"old_text\": \"foo\", \"new_text\": \"bar\"}}\n</invoke>"
	calls, visible := ParseToolCalls(input)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if visible != "" {
		t.Fatalf("expected no visible text, got %q", visible)
	}
	if calls[0].Name != "edit_file" {
		t.Errorf("name = %q", calls[0].Name)
	}
	if calls[0].Args["path"] != "main.go" {
		t.Errorf("path = %v", calls[0].Args["path"])
	}
}
