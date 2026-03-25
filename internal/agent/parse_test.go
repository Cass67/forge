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

func TestParseToolCallWithoutOpeningTag(t *testing.T) {
	input := "{\"name\": \"list_dir\", \"args\": {\"path\": \".\", \"recursive\": false}}</tool_call>"
	calls, visible := ParseToolCalls(input)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "list_dir" {
		t.Fatalf("name = %q", calls[0].Name)
	}
	if got := calls[0].Args["path"]; got != "." {
		t.Fatalf("path = %v", got)
	}
	if visible != "" {
		t.Fatalf("visible = %q, want empty", visible)
	}
}

func TestParseInlineToolCallWithVisiblePrefix(t *testing.T) {
	input := "Reviewing the repo structure now.<tool_call>{\"name\":\"list_dir\",\"args\":{\"path\":\".\",\"recursive\":false}}</tool_call>"
	calls, visible := ParseToolCalls(input)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "list_dir" {
		t.Fatalf("name = %q", calls[0].Name)
	}
	if got := calls[0].Args["path"]; got != "." {
		t.Fatalf("path = %v", got)
	}
	if visible != "Reviewing the repo structure now." {
		t.Fatalf("visible = %q, want %q", visible, "Reviewing the repo structure now.")
	}
}

func TestParseLooseToolCallWithVisibleSuffix(t *testing.T) {
	input := "{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\",\"start_line\":1,\"end_line\":260}}I need a bit more repository evidence before I can give a reliable overview."
	calls, visible := ParseToolCalls(input)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("name = %q", calls[0].Name)
	}
	if got := calls[0].Args["path"]; got != "README.md" {
		t.Fatalf("path = %v", got)
	}
	if got := calls[0].Args["start_line"]; got != float64(1) {
		t.Fatalf("start_line = %v", got)
	}
	if got := calls[0].Args["end_line"]; got != float64(260) {
		t.Fatalf("end_line = %v", got)
	}
	if visible != "I need a bit more repository evidence before I can give a reliable overview." {
		t.Fatalf("visible = %q", visible)
	}
}

func TestParseOpenTaggedToolCallWithVisibleSuffixAndNoCloser(t *testing.T) {
	input := "<tool_call>{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\",\"start_line\":1,\"end_line\":260}}I need a bit more repository evidence before I can give a reliable overview."
	calls, visible := ParseToolCalls(input)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("name = %q", calls[0].Name)
	}
	if got := calls[0].Args["path"]; got != "README.md" {
		t.Fatalf("path = %v", got)
	}
	if got := calls[0].Args["start_line"]; got != float64(1) {
		t.Fatalf("start_line = %v", got)
	}
	if got := calls[0].Args["end_line"]; got != float64(260) {
		t.Fatalf("end_line = %v", got)
	}
	if visible != "I need a bit more repository evidence before I can give a reliable overview." {
		t.Fatalf("visible = %q", visible)
	}
}

func TestParseStructuredDelegateEnvelopeIsNotToolCall(t *testing.T) {
	input := `{"status":"complete","message":"Prepared findings.","artifact_kind":"summary","artifact":"body","next_role":"","next_task":""}`
	calls, visible := ParseToolCalls(input)
	if len(calls) != 0 {
		t.Fatalf("expected no calls, got %d", len(calls))
	}
	if visible != input {
		t.Fatalf("visible = %q, want original input", visible)
	}
}
