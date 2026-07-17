package agent

import (
	"strings"
	"testing"
)

func TestProgressLineCoversCoreTools(t *testing.T) {
	t.Parallel()

	cases := []struct {
		tool    string
		summary string
	}{
		{tool: "list_dir", summary: "."},
		{tool: "read_file", summary: "internal/harness/runner.go"},
		{tool: "search", summary: "preview"},
		{tool: "glob", summary: "*.go"},
		{tool: "edit_file", summary: "internal/tui/chatmodel.go"},
		{tool: "write_file", summary: "themes_preview.html"},
		{tool: "artifact_write", summary: "themes_preview.html"},
		{tool: "artifact_read", summary: "artifact://themes-preview"},
		{tool: "preview_server_ensure", summary: "themes_preview.html"},
		{tool: "preview_server_status", summary: ""},
		{tool: "run_command", summary: "go test ./..."},
		{tool: "tool_help", summary: "preview"},
		{tool: "web_fetch", summary: "https://example.com"},
		{tool: "web_search", summary: "forge harness progress events"},
		{tool: "git_status", summary: ""},
		{tool: "git_diff", summary: "HEAD"},
		{tool: "git_log", summary: "5"},
		{tool: "git_commit", summary: "fix progress updates"},
		{tool: "think", summary: "plan the next step"},
		{tool: "spawn_agent", summary: "audit the repository"},
		{tool: "wait_agent", summary: "agent-1"},
		{tool: "delegate", summary: "builder"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()
			got := strings.TrimSpace(progressLine("strictlocal", tc.tool, tc.summary))
			if got == "" {
				t.Fatalf("progress line missing for tool %q", tc.tool)
			}
		})
	}
}
