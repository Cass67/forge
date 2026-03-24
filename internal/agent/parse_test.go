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

func TestParseFunctionCallsFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     int
		wantName string
	}{
		{
			name:     "function_calls with JSON array",
			input:    "I'll delegate.\n\n<function_calls>\n[{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"find stuff\"}}]\n</function_calls>",
			want:     1,
			wantName: "delegate",
		},
		{
			name:     "function_calls with single JSON object",
			input:    "<function_calls>\n{\"name\": \"read_file\", \"args\": {\"path\": \"main.go\"}}\n</function_calls>",
			want:     1,
			wantName: "read_file",
		},
		{
			name:     "function_calls with multiple items in array",
			input:    "<function_calls>\n[{\"name\": \"read_file\", \"args\": {\"path\": \"a.go\"}}, {\"name\": \"search\", \"args\": {\"pattern\": \"foo\"}}]\n</function_calls>",
			want:     2,
			wantName: "read_file",
		},
		{
			name:     "tool_calls variant",
			input:    "<tool_calls>\n{\"name\": \"glob\", \"args\": {\"pattern\": \"*.go\"}}\n</tool_calls>",
			want:     1,
			wantName: "glob",
		},
		{
			name:     "invoke XML format",
			input:    "<function_calls>\n<invoke name=\"delegate\">\n<parameter name=\"role\">scout</parameter>\n<parameter name=\"task\">audit code</parameter>\n</invoke>\n</function_calls>",
			want:     1,
			wantName: "delegate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, _ := ParseToolCalls(tt.input)
			if len(calls) != tt.want {
				t.Errorf("got %d tool calls, want %d", len(calls), tt.want)
			}
			if tt.want > 0 && calls[0].Name != tt.wantName {
				t.Errorf("name = %q, want %q", calls[0].Name, tt.wantName)
			}
		})
	}
}

func TestParseInvokeXMLArgs(t *testing.T) {
	input := "<function_calls>\n<invoke name=\"delegate\">\n<parameter name=\"role\">scout</parameter>\n<parameter name=\"task\">audit the codebase</parameter>\n</invoke>\n</function_calls>"
	calls, _ := ParseToolCalls(input)
	if len(calls) != 1 {
		t.Fatal("expected 1 call")
	}
	if calls[0].Args["role"] != "scout" {
		t.Errorf("role = %v", calls[0].Args["role"])
	}
	if calls[0].Args["task"] != "audit the codebase" {
		t.Errorf("task = %v", calls[0].Args["task"])
	}
}
