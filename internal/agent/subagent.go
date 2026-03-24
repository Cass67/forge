package agent

import (
	"context"
	"fmt"

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
	}

	err := sub.Run(subCtx, task)

	if err != nil && subCtx.Err() != nil {
		subRenderer.Info(fmt.Sprintf("[%s] cancelled", role))
		return fmt.Sprintf("CANCELLED: %s was cancelled by user. Present what you have or re-delegate.", role), nil
	}

	result := sub.lastFullResponse
	if result == "" {
		result = "(sub-agent produced no output)"
	}

	subRenderer.Info(fmt.Sprintf("[%s] done", role))

	if err != nil {
		return fmt.Sprintf("AGENT ERROR (%s): %v\n\nPartial output:\n%s", role, err, result), nil
	}
	return result, nil
}
