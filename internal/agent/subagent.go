package agent

import (
	"context"
	"fmt"
	"strings"

	"forge/internal/agent/tools"
	"forge/internal/llm"
)

// DriverFactory creates a driver for a given model name.
// Returns nil if the model is unavailable.
type DriverFactory func(modelName string) llm.Driver

// MultiAgentConfig holds the configuration for multi-agent delegation.
type MultiAgentConfig struct {
	// Enabled turns on multi-agent mode (dispatch orchestrates sub-agents).
	Enabled bool
	// RoleModels maps role names to preferred model names.
	// Empty string means use the default chat model.
	RoleModels map[string]string
	// MakeDriver creates a driver for a model name.
	MakeDriver DriverFactory
	// BaseTools is the full tool registry before filtering.
	BaseTools *tools.Registry
}

// SpawnSubAgent creates a sub-agent with the given role, runs it with the task,
// and returns its final response.
func (a *Agent) SpawnSubAgent(ctx context.Context, role, task string, mac MultiAgentConfig) (string, error) {
	roleDef, ok := Roles[role]
	if !ok {
		return "", fmt.Errorf("unknown agent role: %q", role)
	}

	// Resolve driver: role-specific model, or fall back to parent's driver.
	driver := a.driver
	if mac.MakeDriver != nil {
		if modelName, ok := mac.RoleModels[role]; ok && modelName != "" {
			if d := mac.MakeDriver(modelName); d != nil {
				driver = d
			}
		}
	}

	// Filter tools for this role.
	filteredTools := mac.BaseTools.Filter(roleDef.AllowTools)

	// Build system prompt for the sub-agent.
	system := BuildSystemPrompt(a.workDir, filteredTools, "") + "\n\n" + roleDef.System

	// Create a sub-agent renderer that tags events with the role name.
	var subRenderer RenderTarget
	if evr, ok := a.renderer.(*EventRenderer); ok {
		subRenderer = NewSubAgentRenderer(evr, role)
	} else {
		subRenderer = a.renderer
	}

	// Create a cancellable child context for the sub-agent.
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	a.mu.Lock()
	a.activeSubCancel = subCancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.activeSubCancel = nil
		a.mu.Unlock()
	}()

	// Notify both chat (parent renderer) and tools pane (sub renderer).
	a.renderer.Info(fmt.Sprintf("delegating to %s", role))
	subRenderer.Info(fmt.Sprintf("[%s] starting", role))

	sub := &Agent{
		driver:     driver,
		tools:      filteredTools,
		approve:    a.approve,
		workDir:    a.workDir,
		maxTurns:   roleDef.MaxTurns,
		renderer:   subRenderer,
		system:     system,
		isSubAgent: true,
		role:       role,
	}

	err := sub.Run(subCtx, task)
	result := subAgentFinalResult(role, sub)
	if err == nil {
		result, err = retryStructuredSubAgentResult(subCtx, sub, role, result)
	}

	if err != nil && subCtx.Err() != nil {
		subRenderer.Info(fmt.Sprintf("[%s] cancelled", role))
		return fmt.Sprintf("CANCELLED: %s was cancelled by user. Present what you have or re-delegate.", role), nil
	}

	subRenderer.Info(fmt.Sprintf("[%s] done", role))

	if err != nil {
		return fmt.Sprintf("AGENT ERROR (%s): %v\n\nPartial output:\n%s", role, err, result), nil
	}
	return result, nil
}

func subAgentFinalResult(role string, sub *Agent) string {
	result := sub.lastFullResponse
	if strings.TrimSpace(result) == "" {
		return fmt.Sprintf("AGENT ERROR (%s): produced no final output", role)
	}
	return result
}

func retryStructuredSubAgentResult(ctx context.Context, sub *Agent, role, result string) (string, error) {
	if !subAgentNeedsStructuredRetry(role, result) {
		return result, nil
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := sub.Run(ctx, subAgentStructuredOutputNudgeMessage(role, attempt)); err != nil {
			return subAgentFinalResult(role, sub), err
		}
		result = subAgentFinalResult(role, sub)
		if !subAgentNeedsStructuredRetry(role, result) {
			return result, nil
		}
	}
	return result, fmt.Errorf("%s produced unstructured final output after structured-output retry", role)
}

func subAgentNeedsStructuredRetry(role, result string) bool {
	if !roleRequiresStructuredDelegateResult(role) {
		return false
	}
	if _, ok := parseDelegateEnvelope(result); ok {
		return false
	}
	outcome := parseDelegateOutcomeForRole(role, result)
	return outcome.Completed()
}

func roleRequiresStructuredDelegateResult(role string) bool {
	switch strings.TrimSpace(role) {
	case "scout", "builder", "doctor", "architect":
		return true
	default:
		return false
	}
}

func subAgentStructuredOutputNudgeMessage(role string, attempt int) string {
	role = strings.TrimSpace(role)
	if role == "" {
		role = "sub-agent"
	}
	switch attempt {
	case 1:
		return fmt.Sprintf("%s final output must be exactly one JSON object with status, message, artifact_kind, artifact, next_role, and next_task. Use status \"complete\" or \"blocked\" only. Do not call tools. No prose outside the JSON object.", role)
	default:
		return fmt.Sprintf("Still unstructured. %s must re-emit the completed answer as exactly one JSON object with status, message, artifact_kind, artifact, next_role, and next_task. Use status \"complete\" or \"blocked\" only. No prose outside the JSON object.", role)
	}
}
