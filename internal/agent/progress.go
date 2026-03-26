package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

// progressLine generates a human-readable one-liner for a sub-agent tool call.
func progressLine(role, toolName, summary string) string {
	_ = role
	summary = strings.TrimSpace(summary)
	switch toolName {
	case "read_file":
		return fmt.Sprintf("reading %s", filepath.Base(summary))
	case "search":
		return fmt.Sprintf("searching for %q", summary)
	case "glob":
		return fmt.Sprintf("looking for %q", summary)
	case "edit_file":
		return fmt.Sprintf("editing %s", filepath.Base(summary))
	case "write_file":
		return fmt.Sprintf("writing %s", filepath.Base(summary))
	case "run_command":
		cmd := summary
		if len(cmd) > 40 {
			cmd = cmd[:40] + "..."
		}
		return fmt.Sprintf("running %s", cmd)
	case "delegate":
		return delegateProgressLine(summary)
	default:
		return "working"
	}
}

func delegateProgressLine(summary string) string {
	switch strings.ToLower(strings.TrimSpace(summary)) {
	case "builder", "editor":
		return "making the change"
	case "doctor", "verifier":
		return "checking the issue"
	case "architect":
		return "thinking through the approach"
	default:
		return "reviewing the repo"
	}
}
