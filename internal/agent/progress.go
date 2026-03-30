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
			return "Getting the lay of the land in the workspace"
		}
		return fmt.Sprintf("Scanning %s to map the structure", filepath.Base(summary))
	case "read_file":
		return readFileProgressLine(summary)
	case "search":
		return searchProgressLine(summary)
	case "glob":
		return fmt.Sprintf("Finding files that match %q", summary)
	case "edit_file":
		return fmt.Sprintf("Updating %s", filepath.Base(summary))
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
		return "Checking current working-tree changes"
	case "git_diff":
		return "Reviewing code changes in the diff"
	case "git_log":
		return "Looking at recent commits for context"
	case "git_commit":
		return "Creating a commit"
	case "list_mcp_resources":
		return "Listing MCP resources"
	case "list_mcp_resource_templates":
		return "Listing MCP resource templates"
	case "read_mcp_resource":
		return "Reading MCP resource content"
	case "think":
		return "Reasoning through the next step"
	case "delegate":
		return delegateProgressLine(summary)
	default:
		if strings.HasPrefix(toolName, "mcp__") {
			return mcpProgressLine(toolName)
		}
		return ""
	}
}

func readFileProgressLine(summary string) string {
	base := filepath.Base(strings.TrimSpace(summary))
	switch strings.ToLower(base) {
	case "readme.md", "readme":
		return "Reading README to understand the repository intent"
	case "agents.md":
		return "Reviewing project instructions before proceeding"
	case ".gitignore":
		return "Checking ignore rules and repo hygiene"
	default:
		if base == "" || base == "." {
			return "Reading a file for context"
		}
		return fmt.Sprintf("Reading %s for context", base)
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

func mcpProgressLine(toolName string) string {
	parts := strings.Split(toolName, "__")
	if len(parts) < 3 {
		return "Calling an MCP tool"
	}
	server := strings.TrimSpace(parts[1])
	tool := strings.TrimSpace(parts[2])
	if server == "" || tool == "" {
		return "Calling an MCP tool"
	}
	return fmt.Sprintf("Calling MCP %s:%s", server, tool)
}
