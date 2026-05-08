package reacttools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agenttools "forge/internal/agent/tools"
	"forge/internal/react"
)

func NewSpawnAgent(pool *react.AgentPool) agenttools.Tool {
	desc := "Spawn a bounded child agent task. Role is optional advisory context."
	if pool != nil {
		if names := pool.AgentNames(); len(names) > 0 {
			desc += " Available agents: " + strings.Join(names, ", ")
		}
	}
	desc += " Read-only agents such as repo-auditor, code-reviewer, explorer, oracle, and synthesizer must only inspect/analyze and return content; the parent agent must create files, edit files, or run commands."
	return agenttools.Tool{
		Name:        "spawn_agent",
		Description: desc,
		Parameters: []agenttools.ParameterDef{
			{Name: "task_description", Type: "string", Description: "Task to delegate to the child agent", Required: true},
			{Name: "role", Type: "string", Description: "Optional role hint for the child agent", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			if pool == nil {
				return "", fmt.Errorf("agent pool unavailable")
			}
			task, _ := args["task_description"].(string)
			if strings.TrimSpace(task) == "" {
				return "", fmt.Errorf("task_description is required")
			}
			role, _ := args["role"].(string)
			if strings.TrimSpace(role) == "" {
				role = "default"
			}
			mappedRole := react.MapSpawnRole(role)
			if agentDef, ok := pool.GetAgent(mappedRole); ok && agentDef != nil && readOnlyAgentShouldSanitizeTask(agentDef, task) {
				task = sanitizeReadOnlyAgentTask(task)
			}

			id, err := pool.Spawn(ctx, role, task)
			if err != nil {
				return "", err
			}
			result := react.AgentResult{
				ID:     id,
				Role:   role,
				Status: react.AgentStatusRunning,
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return "", err
			}
			return string(encoded), nil
		},
	}
}

func readOnlyAgentShouldSanitizeTask(agentDef *react.AgentDefinition, task string) bool {
	if agentDef == nil || len(agentDef.Tools) == 0 {
		return false
	}
	if agentHasMutationTools(agentDef.Tools) {
		return false
	}
	return taskRequestsMutation(task)
}

func sanitizeReadOnlyAgentTask(task string) string {
	task = strings.TrimSpace(task)
	return strings.Join([]string{
		"Inspect/analyze only. Do not create files, edit files, run shell commands, or commit changes.",
		"If the request asks for a report or file, return the complete report content and the intended path so the parent agent can save it.",
		"Original delegated context:",
		task,
	}, "\n")
}

func agentHasMutationTools(tools []string) bool {
	for _, name := range tools {
		switch strings.TrimSpace(name) {
		case "write_file", "edit_file", "apply_patch", "artifact_write", "run_command", "exec_session_start", "command_write_stdin":
			return true
		}
	}
	return false
}

func taskRequestsMutation(task string) bool {
	text := strings.ToLower(strings.TrimSpace(task))
	if text == "" {
		return false
	}
	mutationPhrases := []string{
		"write_file", "edit_file", "apply_patch", "run_command", "mkdir",
		"create file", "create the file", "file creation", "create docs/", "create a doc", "create a report",
		"write file", "write the file", "write to ", "write docs/", "write a doc", "write a report", "write a new report",
		"save to ", "save the file", "generate docs/", "append to ", "update file", "edit file", "report path", "intended report path",
	}
	for _, phrase := range mutationPhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return strings.Contains(text, ".md") && strings.Contains(text, "docs/") && strings.Contains(text, "create")
}
