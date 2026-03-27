package agent

import "testing"

func TestProgressLine(t *testing.T) {
	tests := []struct {
		role, tool, summary string
		want                string
	}{
		{"scout", "read_file", "/Users/x/main.go", "Reading main.go"},
		{"scout", "search", "session", `Searching for "session"`},
		{"scout", "glob", "*.go", `Looking for "*.go"`},
		{"builder", "edit_file", "/Users/x/main.go", "Editing main.go"},
		{"builder", "run_command", "go build ./...", "Running go build ./..."},
		{"builder", "run_command", "very long command that exceeds the forty character limit here", "Running very long command that exceeds the forty..."},
		{"builder", "write_file", "/Users/x/new.go", "Writing new.go"},
		{"builder", "preview_server_ensure", "themes_preview.html", "Starting the preview for themes_preview.html"},
		{"builder", "preview_server_status", "", "Checking the preview status"},
		{"builder", "tool_help", "brainstorming", "Checking available tools"},
		{"dispatch", "delegate", "scout", "Reviewing the repo"},
		{"scout", "unknown_tool", "whatever", ""},
	}
	for _, tt := range tests {
		t.Run(tt.tool+"_"+tt.role, func(t *testing.T) {
			got := progressLine(tt.role, tt.tool, tt.summary)
			if got != tt.want {
				t.Errorf("progressLine(%q, %q, %q) = %q, want %q", tt.role, tt.tool, tt.summary, got, tt.want)
			}
		})
	}
}
