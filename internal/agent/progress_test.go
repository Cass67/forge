package agent

import "testing"

func TestProgressLine(t *testing.T) {
	tests := []struct {
		role, tool, summary string
		want                string
	}{
		{"scout", "read_file", "/Users/x/main.go", "scout: reading main.go"},
		{"scout", "search", "session", `scout: searching for "session"`},
		{"scout", "glob", "*.go", `scout: finding "*.go"`},
		{"builder", "edit_file", "/Users/x/main.go", "builder: editing main.go"},
		{"builder", "run_command", "go build ./...", "builder: running go build ./..."},
		{"builder", "run_command", "very long command that exceeds the forty character limit here", "builder: running very long command that exceeds the forty..."},
		{"builder", "write_file", "/Users/x/new.go", "builder: writing new.go"},
		{"dispatch", "delegate", "scout", "dispatching to scout"},
		{"scout", "unknown_tool", "whatever", "scout: unknown_tool"},
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
