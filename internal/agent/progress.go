package agent

import (
	"fmt"
	"path/filepath"
)

// progressLine generates a human-readable one-liner for a sub-agent tool call.
func progressLine(role, toolName, summary string) string {
	switch toolName {
	case "read_file":
		return fmt.Sprintf("%s: reading %s", role, filepath.Base(summary))
	case "search":
		return fmt.Sprintf("%s: searching for %q", role, summary)
	case "glob":
		return fmt.Sprintf("%s: finding %q", role, summary)
	case "edit_file":
		return fmt.Sprintf("%s: editing %s", role, filepath.Base(summary))
	case "write_file":
		return fmt.Sprintf("%s: writing %s", role, filepath.Base(summary))
	case "run_command":
		cmd := summary
		if len(cmd) > 40 {
			cmd = cmd[:40] + "..."
		}
		return fmt.Sprintf("%s: running %s", role, cmd)
	case "delegate":
		return fmt.Sprintf("dispatching to %s", summary)
	default:
		return fmt.Sprintf("%s: %s", role, toolName)
	}
}
