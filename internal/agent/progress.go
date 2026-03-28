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
			return "Listing workspace files"
		}
		return fmt.Sprintf("Listing %s", filepath.Base(summary))
	case "read_file":
		return fmt.Sprintf("Reading %s", filepath.Base(summary))
	case "search":
		return fmt.Sprintf("Searching for %q", summary)
	case "glob":
		return fmt.Sprintf("Looking for %q", summary)
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
		return "Checking the preview status"
	case "run_command":
		cmd := summary
		if len(cmd) > 40 {
			cmd = cmd[:40] + "..."
		}
		return fmt.Sprintf("Running %s", cmd)
	case "tool_help":
		return "Checking available tools"
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
		return "Reviewing commit history"
	case "git_commit":
		return "Creating a commit"
	case "think":
		return "Thinking through the next step"
	case "delegate":
		return delegateProgressLine(summary)
	default:
		return ""
	}
}

func delegateProgressLine(summary string) string {
	switch strings.ToLower(strings.TrimSpace(summary)) {
	case "builder", "editor":
		return "Making the change"
	case "doctor", "verifier":
		return "Checking the issue"
	case "architect":
		return "Thinking through the approach"
	default:
		return "Reviewing the repo"
	}
}
