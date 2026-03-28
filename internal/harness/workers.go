package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"forge/internal/agent"
	"forge/internal/agent/tools"
	"forge/internal/chatstate"
	"forge/internal/llm"
	"forge/internal/skills"
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

	profile := append([]string(nil), task.PermissionProfile...)
	if len(profile) == 0 {
		profile = workerToolAllowlist(task.Kind)
	}
	reg := tools.NewRegistry()
	if m.baseTools != nil {
		reg = m.baseTools.Filter(profile)
	}
	loadedSkills := append([]skills.Skill(nil), task.SkillContext.Loaded...)
	worker := agent.NewAgent(driver, reg, m.approve, m.workDir, workerMaxTurns(task.Kind), agent.NewHiddenWorkerRenderer(nil), loadedSkills, chatstate.New())
	worker.SetRole(string(task.Kind))
	worker.SetSubAgentMode(true)
	worker.SetSystem(agent.BuildWorkerSystemPrompt(m.workDir, reg, string(task.Kind), loadedSkills))

	skillRuntime := skills.NewRuntime(loadedSkills)
	if err := applyWorkerSkillContext(worker, skillRuntime, task); err != nil {
		return blockedWorkerObservation(task, err, skillRuntime.UseRecords(), nil)
	}

	var raw string
	var validationErr error
	var cumulativeCalls []agent.ToolCall
	for attempt := 0; attempt < 3; attempt++ {
		prompt := buildWorkerPrompt(task)
		if attempt > 0 {
			prompt = workerRetryPrompt(task, validationErr)
		}
		if err := worker.Run(ctx, prompt); err != nil {
			return blockedWorkerObservation(task, err, skillRuntime.UseRecords(), mapObservedToolCalls(cumulativeCalls))
		}
		raw = worker.LastResponse()
		cumulativeCalls = append(cumulativeCalls, worker.LastToolCalls()...)
		validated, err := ValidateWorkerResultWithToolCalls(task, raw, cumulativeCalls)
		if err == nil {
			return Observation{
				Status:    validated.Status,
				Summary:   validated.Summary,
				TopicKey:  task.TopicKey,
				Artifact:  validated.Parsed,
				ToolCalls: mapObservedToolCalls(cumulativeCalls),
				SkillUses: skillRuntime.UseRecords(),
			}, nil
		}
		validationErr = err
	}

	err := fmt.Errorf("%s produced invalid structured output after retries", task.Kind)
	return blockedWorkerObservation(task, err, skillRuntime.UseRecords(), mapObservedToolCalls(cumulativeCalls))
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
	if len(task.PermissionProfile) > 0 {
		sb.WriteString("\nALLOWED TOOLS: " + strings.Join(task.PermissionProfile, ", "))
	}
	if task.RequireRepresentativeFileEvidence {
		sb.WriteString("\nREQUIRED EVIDENCE: grounded representative file via read_file")
	}
	if task.RequireNonReadmeFileEvidence {
		sb.WriteString("\nREQUIRED EVIDENCE: grounded non-README file via read_file")
	}
	if !task.Deadline.IsZero() {
		sb.WriteString("\nDEADLINE: " + task.Deadline.UTC().Format(time.RFC3339))
	}
	if task.EvidenceBudget > 0 {
		sb.WriteString(fmt.Sprintf("\nEVIDENCE BUDGET: %d", task.EvidenceBudget))
	}
	return sb.String()
}

func workerRetryPrompt(task WorkerTask, cause error) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Your previous %s output was invalid for the runtime contract.\n", task.Kind)
	if cause != nil {
		fmt.Fprintf(&sb, "Validation error: %s\n", strings.TrimSpace(cause.Error()))
		if needsJSONStringEscapeGuidance(cause) {
			sb.WriteString("JSON strings must escape control characters. Replace raw tabs/newlines with escaped forms (\\\\t, \\\\n) and return one compact JSON object.\n")
		}
	}
	sb.WriteString("Return to the strict worker state machine:\n")
	sb.WriteString("- if you still need more information or work, emit exactly one tool call and nothing else\n")
	sb.WriteString("- if you are fully done, emit exactly one valid JSON object and nothing else\n")
	if task.Kind == WorkerReader {
		sb.WriteString("- do not cite file evidence unless you actually called read_file on that exact path\n")
	}
	if task.Kind == WorkerReader && readerTaskRequiresRepresentativeFile(task) {
		sb.WriteString("- this walkthrough is not complete until you inspect at least one representative file with read_file and include grounded file evidence\n")
	}
	if task.Kind == WorkerReader && readerTaskRequiresNonReadmeFile(task) {
		sb.WriteString("- this evaluative review is not complete until you inspect at least one grounded non-README implementation, config, or entrypoint file\n")
	}
	sb.WriteString("Do not repeat the previous invalid format.\n")
	return strings.TrimSpace(sb.String())
}

func needsJSONStringEscapeGuidance(cause error) bool {
	if cause == nil {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(cause.Error()))
	if !strings.Contains(lower, "string literal") {
		return false
	}
	return strings.Contains(lower, "\\t") ||
		strings.Contains(lower, "\\n") ||
		strings.Contains(lower, "control character")
}

func applyWorkerSkillContext(worker *agent.Agent, runtime *skills.Runtime, task WorkerTask) error {
	if worker == nil || runtime == nil {
		return nil
	}

	requiredName := skills.RequiredForInput(task.Objective)
	injected := make(map[string]struct{})
	if requiredName != "" {
		required, ok := runtime.ResolveRequired(task.Objective)
		if !ok {
			runtime.RecordSkillUse(requiredName, string(task.Kind), "required_missing")
			return fmt.Errorf("required skill unavailable: %s", requiredName)
		}
		worker.InjectSkill(required)
		runtime.RecordSkillUse(required.Name, string(task.Kind), "required_applied")
		injected[required.Name] = struct{}{}
	}

	if skills.NormalizeAutoMode(task.SkillContext.AutoMode) != skills.AutoSkillsAuto {
		return nil
	}
	autoSkill, ok := runtime.ResolveAuto(task.Objective)
	if !ok {
		return nil
	}
	if _, seen := injected[autoSkill.Name]; seen {
		return nil
	}
	worker.InjectSkill(autoSkill)
	runtime.RecordSkillUse(autoSkill.Name, string(task.Kind), "auto_applied")
	return nil
}

func blockedWorkerObservation(task WorkerTask, err error, uses []skills.UseRecord, calls []ObservedToolCall) (Observation, error) {
	summary := firstNonEmpty(errorString(err), "worker failed closed")
	obs := Observation{
		Status:    ObservationBlocked,
		Summary:   summary,
		TopicKey:  task.TopicKey,
		ToolCalls: calls,
		SkillUses: uses,
		Err:       err,
	}
	return obs, err
}
