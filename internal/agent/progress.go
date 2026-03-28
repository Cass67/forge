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
	case "list_dir":
		if summary == "" || summary == "." {
			return "Quick scan of the workspace layout"
		}
		return fmt.Sprintf("Scanning %s", filepath.Base(summary))
	case "read_file":
		return fmt.Sprintf("Reading %s", filepath.Base(summary))
	case "search":
		return searchProgressLine(summary)
	case "glob":
		return fmt.Sprintf("Finding files matching %q", summary)
	case "edit_file":
		return fmt.Sprintf("Editing %s", filepath.Base(summary))
	case "write_file":
		return fmt.Sprintf("Writing %s", filepath.Base(summary))
	case "artifact_write":
		return fmt.Sprintf("Writing %s", filepath.Base(summary))
	case "artifact_read":
		return fmt.Sprintf("Reading %s", filepath.Base(summary))
	case "preview_server_ensure":
		if strings.TrimSpace(summary) == "" {
			return "Starting the preview"
		}
		return fmt.Sprintf("Starting the preview for %s", filepath.Base(summary))
	case "preview_server_status":
		return "Checking preview status"
	case "run_command":
		cmd := summary
		if len(cmd) > 40 {
			cmd = cmd[:40] + "..."
		}
		return fmt.Sprintf("Running %s", cmd)
	case "tool_help":
		return "Checking which tools are available"
	case "web_fetch":
		return fmt.Sprintf("Fetching %q", summary)
	case "web_search":
		return fmt.Sprintf("Searching the web for %q", summary)
	case "scratchpad_write":
		return "Saving notes for this step"
	case "scratchpad_read":
		return "Loading saved notes"
	case "git_status":
		return "Checking git status"
	case "git_diff":
		return "Reviewing git diff"
	case "git_log":
		return "Reviewing recent commits"
	case "git_commit":
		return "Creating a commit"
	case "think":
		return "Reasoning through the next step"
	case "delegate":
		return delegateProgressLine(summary)
	default:
		return ""
	}
}

func delegateProgressLine(summary string) string {
	switch strings.ToLower(strings.TrimSpace(summary)) {
	case "builder", "editor":
		return "Handing implementation to the builder"
	case "doctor", "verifier":
		return "Handing validation to the verifier"
	case "architect":
		return "Asking the architect for a deeper plan"
	default:
		return "Delegating this step for focused work"
	}
}

func searchProgressLine(summary string) string {
	normalized := strings.ToLower(strings.TrimSpace(summary))
	switch {
	case strings.Contains(normalized, "todo"), strings.Contains(normalized, "fixme"), strings.Contains(normalized, "hack"), strings.Contains(normalized, "xxx"):
		return "Scanning for TODO/FIXME markers"
	case normalized == "#!":
		return "Checking script shebang usage"
	case normalized == "print":
		return "Checking for debug print calls"
	case strings.Contains(normalized, "from __future__ import"):
		return "Checking Python compatibility imports"
	default:
		return fmt.Sprintf("Searching for %q", strings.TrimSpace(summary))
	}
}
