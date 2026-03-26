package harness

import (
	"context"
	"fmt"
	"strings"

	"forge/internal/agent"
	"forge/internal/agent/tools"
	"forge/internal/llm"
)

type WorkerDriverResolver func(WorkerKind) llm.Driver

type WorkerExecutor interface {
	Execute(ctx context.Context, task WorkerTask) (Observation, error)
}

type ManagerConfig struct {
	WorkDir     string
	BaseTools   *tools.Registry
	Approve     tools.ApprovalFunc
	DriverFor   WorkerDriverResolver
	DefaultMode llm.Driver
}

type Manager struct {
	workDir   string
	baseTools *tools.Registry
	approve   tools.ApprovalFunc
	driverFor WorkerDriverResolver
}

func NewManager(cfg ManagerConfig) *Manager {
	driverFor := cfg.DriverFor
	if driverFor == nil && cfg.DefaultMode != nil {
		driverFor = func(WorkerKind) llm.Driver {
			return cfg.DefaultMode
		}
	}
	return &Manager{
		workDir:   cfg.WorkDir,
		baseTools: cfg.BaseTools,
		approve:   cfg.Approve,
		driverFor: driverFor,
	}
}

func (m *Manager) Execute(ctx context.Context, task WorkerTask) (Observation, error) {
	if m == nil || m.driverFor == nil {
		return Observation{
			Status:   ObservationBlocked,
			Summary:  "worker manager unavailable",
			TopicKey: task.TopicKey,
			Err:      fmt.Errorf("worker manager unavailable"),
		}, fmt.Errorf("worker manager unavailable")
	}
	driver := m.driverFor(task.Kind)
	if driver == nil {
		return Observation{
			Status:   ObservationBlocked,
			Summary:  "worker driver unavailable",
			TopicKey: task.TopicKey,
			Err:      fmt.Errorf("worker driver unavailable"),
		}, fmt.Errorf("worker driver unavailable")
	}

	reg := tools.NewRegistry()
	if m.baseTools != nil {
		reg = m.baseTools.Filter(workerToolAllowlist(task.Kind))
	}
	worker := agent.NewAgent(driver, reg, m.approve, m.workDir, workerMaxTurns(task.Kind), agent.NewHiddenWorkerRenderer(nil), nil, nil)
	worker.SetRole(string(task.Kind))
	worker.SetSubAgentMode(true)
	worker.SetSystem(agent.BuildWorkerSystemPrompt(m.workDir, reg, string(task.Kind)))

	var raw string
	for attempt := 0; attempt < 3; attempt++ {
		prompt := buildWorkerPrompt(task)
		if attempt > 0 {
			prompt = workerRetryPrompt(task.Kind, raw)
		}
		if err := worker.Run(ctx, prompt); err != nil {
			return Observation{
				Status:   ObservationBlocked,
				Summary:  err.Error(),
				TopicKey: task.TopicKey,
				Err:      err,
			}, err
		}
		raw = worker.LastResponse()
		validated, err := ValidateWorkerResultWithToolCalls(task.Kind, raw, worker.LastToolCalls())
		if err == nil {
			return Observation{
				Status:   validated.Status,
				Summary:  validated.Summary,
				TopicKey: task.TopicKey,
				Artifact: validated.Parsed,
			}, nil
		}
	}

	err := fmt.Errorf("%s produced invalid structured output after retries", task.Kind)
	return Observation{
		Status:   ObservationBlocked,
		Summary:  err.Error(),
		TopicKey: task.TopicKey,
		Err:      err,
	}, err
}

func workerToolAllowlist(kind WorkerKind) []string {
	switch kind {
	case WorkerReader:
		return []string{"read_file", "glob", "search", "list_dir", "git_log", "git_diff", "git_status", "think"}
	case WorkerEditor:
		return []string{"read_file", "write_file", "edit_file", "glob", "search", "list_dir", "run_command", "git_diff", "git_status", "think"}
	case WorkerVerifier:
		return []string{"read_file", "glob", "search", "list_dir", "run_command", "git_diff", "git_status", "git_log", "think"}
	case WorkerResearcher:
		return []string{"read_file", "glob", "search", "list_dir", "web_search", "web_fetch", "run_command", "think"}
	default:
		return nil
	}
}

func workerMaxTurns(kind WorkerKind) int {
	switch kind {
	case WorkerEditor:
		return 20
	case WorkerVerifier:
		return 12
	case WorkerResearcher:
		return 12
	default:
		return 10
	}
}

func buildWorkerPrompt(task WorkerTask) string {
	var sb strings.Builder
	sb.WriteString("OBJECTIVE: " + strings.TrimSpace(task.Objective))
	if strings.TrimSpace(task.Context) != "" {
		sb.WriteString("\nCONTEXT:\n" + strings.TrimSpace(task.Context))
	}
	if strings.TrimSpace(task.StopCondition) != "" {
		sb.WriteString("\nSTOP CONDITION: " + strings.TrimSpace(task.StopCondition))
	}
	if task.EvidenceBudget > 0 {
		sb.WriteString(fmt.Sprintf("\nEVIDENCE BUDGET: %d", task.EvidenceBudget))
	}
	return sb.String()
}

func workerRetryPrompt(kind WorkerKind, raw string) string {
	return strings.TrimSpace(fmt.Sprintf(
		"Your previous %s output was invalid for the runtime contract.\n"+
			"Re-emit exactly one valid JSON object and no prose outside it.\n"+
			"Previous output:\n%s",
		kind, strings.TrimSpace(raw),
	))
}
