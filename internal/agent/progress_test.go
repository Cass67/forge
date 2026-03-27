package agent

import "testing"

func TestProgressLine(t *testing.T) {
	tests := []struct {
		role, tool, summary string
		want                string
	}{
		{"scout", "read_file", "/Users/x/main.go", "reading main.go"},
		{"scout", "search", "session", `searching for "session"`},
		{"scout", "glob", "*.go", `looking for "*.go"`},
		{"builder", "edit_file", "/Users/x/main.go", "editing main.go"},
		{"builder", "run_command", "go build ./...", "running go build ./..."},
		{"builder", "run_command", "very long command that exceeds the forty character limit here", "running very long command that exceeds the forty..."},
		{"builder", "write_file", "/Users/x/new.go", "writing new.go"},
		{"builder", "preview_server_ensure", "themes_preview.html", "starting the preview for themes_preview.html"},
		{"builder", "preview_server_status", "", "checking the preview status"},
		{"builder", "tool_help", "brainstorming", "checking available tools"},
		{"dispatch", "delegate", "scout", "reviewing the repo"},
		{"scout", "unknown_tool", "whatever", "working"},
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
