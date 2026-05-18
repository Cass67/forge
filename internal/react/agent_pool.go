package react

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type AgentStatus string

const (
	AgentStatusPending   AgentStatus = "pending"
	AgentStatusRunning   AgentStatus = "running"
	AgentStatusCompleted AgentStatus = "completed"
	AgentStatusFailed    AgentStatus = "failed"
	AgentStatusKilled    AgentStatus = "killed"
	AgentStatusTimeout   AgentStatus = "timeout"
	AgentStatusNotFound  AgentStatus = "not_found"
)

type SpawnFunc func(ctx context.Context, role, task string) (string, error)

type agentIDContextKey struct{}
type agentWorkDirContextKey struct{}

func AgentIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(agentIDContextKey{}).(string)
	return strings.TrimSpace(id)
}

func ContextWithWorkDir(ctx context.Context, workDir string) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithValue(ctx, agentWorkDirContextKey{}, strings.TrimSpace(workDir))
}

func WorkDirFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	dir, _ := ctx.Value(agentWorkDirContextKey{}).(string)
	return strings.TrimSpace(dir)
}

type AgentResult struct {
	ID              string              `json:"id"`
	Role            string              `json:"role"`
	Status          AgentStatus         `json:"status"`
	Result          string              `json:"result,omitempty"`
	Error           string              `json:"error,omitempty"`
	Handoff         *AgentHandoff       `json:"handoff,omitempty"`
	ResumeSupported bool                `json:"resume_supported"`
	ResumeHint      string              `json:"resume_hint,omitempty"`
	LastToolName    string              `json:"last_tool_name,omitempty"`
	RecentActivity  []AgentTaskActivity `json:"recent_activity,omitempty"`
}

type agentJob struct {
	id      string
	role    string
	status  AgentStatus
	result  string
	err     error
	handoff *AgentHandoff
	cancel  context.CancelFunc
	done    chan struct{}

	lastToolName   string
	recentActivity []AgentTaskActivity
}

type AgentDefinition struct {
	Name         string
	Description  string
	SystemPrompt string
	Model        string
	Fallbacks    []string
	ModelFamily  string
	Tools        []string
	ReadOnly     bool
}

type AgentPool struct {
	mu                sync.Mutex
	next              int
	jobs              map[string]*agentJob
	spawn             SpawnFunc
	agents            map[string]*AgentDefinition
	taskObserver      func(AgentTaskState)
	lifecycleObserver func(AgentTaskState)
	progressObserver  func(id, toolName, summary string, at time.Time)
	currentTurnFunc   func() int
}

func NewAgentPool(spawn SpawnFunc) *AgentPool {
	return &AgentPool{
		jobs:  make(map[string]*agentJob),
		spawn: spawn,
	}
}

func (p *AgentPool) AttachSession(session *Session) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if session == nil {
		p.taskObserver = nil
		p.progressObserver = nil
		p.currentTurnFunc = nil
		return
	}
	p.taskObserver = session.UpsertAgentTask
	p.progressObserver = session.RecordAgentTaskProgress
	p.currentTurnFunc = func() int { return session.Snapshot().Turn }
}

func (p *AgentPool) Spawn(ctx context.Context, role, task string) (string, error) {
	if p == nil || p.spawn == nil {
		return "", fmt.Errorf("agent pool spawn function is not configured")
	}
	role = strings.TrimSpace(role)
	if role == "" {
		role = "default"
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return "", fmt.Errorf("task is required")
	}

	now := time.Now()
	p.mu.Lock()
	p.next++
	id := fmt.Sprintf("agent-%d", p.next)
	runCtx := context.Background()
	if ctx != nil {
		runCtx = ctx
	}
	runCtx, runCancel := context.WithCancel(context.WithValue(runCtx, agentIDContextKey{}, id))
	job := &agentJob{
		id:     id,
		role:   role,
		status: AgentStatusRunning,
		cancel: runCancel,
		done:   make(chan struct{}),
	}
	p.jobs[id] = job
	parentTurn := 0
	if p.currentTurnFunc != nil {
		parentTurn = p.currentTurnFunc()
	}
	p.mu.Unlock()

	p.notifyAgentTask(AgentTaskState{
		ID:             id,
		Role:           role,
		Description:    firstAgentTaskLine(task),
		Prompt:         task,
		Status:         AgentStatusRunning,
		CreatedAt:      now,
		StartedAt:      now,
		LastActivityAt: now,
		ParentTurn:     parentTurn,
	})

	go p.runSpawn(runCtx, job, role, task)
	return id, nil
}

func (p *AgentPool) SetLifecycleObserver(observer func(AgentTaskState)) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.lifecycleObserver = observer
	p.mu.Unlock()
}

func (p *AgentPool) runSpawn(runCtx context.Context, job *agentJob, role, task string) {
	result, err := p.spawn(runCtx, role, task)
	completedAt := time.Now()
	var handoff *AgentHandoff
	if err == nil {
		report, parsed, parseErr := ParseAgentHandoff(result)
		if parseErr != nil {
			err = parseErr
		} else {
			result = report
			if !parsed.Empty() {
				result = sanitizeAgentReportForHandoff(report, parsed)
				handoff = &parsed
			}
		}
	}

	p.mu.Lock()
	job.result = strings.TrimSpace(result)
	job.err = err
	job.handoff = cloneAgentHandoff(handoff)
	if job.status == AgentStatusKilled {
		job.result = ""
	} else if err != nil {
		job.status = AgentStatusFailed
	} else {
		job.status = AgentStatusCompleted
	}
	state := AgentTaskState{
		ID:             job.id,
		Role:           job.role,
		Status:         job.status,
		CompletedAt:    completedAt,
		LastActivityAt: completedAt,
		Result:         job.result,
		Handoff:        cloneAgentHandoff(job.handoff),
	}
	if job.err != nil {
		state.Error = job.err.Error()
	}
	p.mu.Unlock()
	p.notifyAgentTask(state)
	close(job.done)
}

func (p *AgentPool) Wait(ctx context.Context, id string, timeout time.Duration) (AgentResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return AgentResult{}, fmt.Errorf("id is required")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	job := p.job(id)
	if job == nil {
		result := decorateAgentResultResumeState(AgentResult{ID: id, Status: AgentStatusNotFound})
		p.notifyAgentTask(AgentTaskState{ID: id, Status: AgentStatusNotFound, LastActivityAt: time.Now()})
		return result, nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-job.done:
		return p.snapshot(job), nil
	default:
		current := p.snapshot(job)
		if agentWaitStatusIsTerminal(current.Status) {
			return current, nil
		}
	}

	select {
	case <-job.done:
		return p.snapshot(job), nil
	case <-timer.C:
		now := time.Now()
		p.mu.Lock()
		result := p.snapshotLocked(job)
		p.mu.Unlock()
		p.notifyAgentTask(AgentTaskState{ID: id, Role: result.Role, Status: result.Status, LastActivityAt: now})
		return result, nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			p.mu.Lock()
			result := p.snapshotLocked(job)
			p.mu.Unlock()
			return result, nil
		}
		return AgentResult{}, ctx.Err()
	}
}

func (p *AgentPool) Statuses() []AgentResult {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]string, 0, len(p.jobs))
	for id := range p.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	results := make([]AgentResult, 0, len(ids))
	for _, id := range ids {
		results = append(results, p.snapshotLocked(p.jobs[id]))
	}
	return results
}

func (p *AgentPool) Kill(ctx context.Context, id string) (AgentResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return AgentResult{}, fmt.Errorf("id is required")
	}
	if p == nil {
		return decorateAgentResultResumeState(AgentResult{ID: id, Status: AgentStatusNotFound}), nil
	}
	now := time.Now()
	p.mu.Lock()
	job := p.jobs[id]
	if job == nil {
		p.mu.Unlock()
		result := decorateAgentResultResumeState(AgentResult{ID: id, Status: AgentStatusNotFound})
		p.notifyAgentTask(AgentTaskState{ID: id, Status: AgentStatusNotFound, LastActivityAt: now})
		return result, nil
	}
	if agentWaitStatusIsTerminal(job.status) {
		result := p.snapshotLocked(job)
		p.mu.Unlock()
		return result, nil
	}
	job.status = AgentStatusKilled
	job.result = ""
	job.err = context.Canceled
	cancel := job.cancel
	result := p.snapshotLocked(job)
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	p.notifyAgentTask(AgentTaskState{ID: id, Role: result.Role, Status: AgentStatusKilled, CompletedAt: now, LastActivityAt: now, Error: result.Error})
	return result, nil
}

func (p *AgentPool) RecordProgress(id, toolName, summary string) {
	id = strings.TrimSpace(id)
	toolName = strings.TrimSpace(toolName)
	if p == nil || id == "" || toolName == "" {
		return
	}
	now := time.Now()
	p.mu.Lock()
	job := p.jobs[id]
	observer := p.progressObserver
	lifecycleObserver := p.lifecycleObserver
	var state AgentTaskState
	if job != nil {
		job.lastToolName = toolName
		job.recentActivity = append(job.recentActivity, AgentTaskActivity{ToolName: toolName, Summary: strings.TrimSpace(summary), At: now})
		if len(job.recentActivity) > 12 {
			job.recentActivity = append([]AgentTaskActivity(nil), job.recentActivity[len(job.recentActivity)-12:]...)
		}
		state = AgentTaskState{
			ID:             job.id,
			Role:           job.role,
			Status:         job.status,
			LastActivityAt: now,
			Result:         job.result,
			LastToolName:   job.lastToolName,
			RecentActivity: cloneAgentTaskActivity(job.recentActivity),
		}
		if job.err != nil {
			state.Error = job.err.Error()
		}
	}
	p.mu.Unlock()
	if observer != nil {
		observer(id, toolName, summary, now)
	}
	if lifecycleObserver != nil && state.ID != "" {
		lifecycleObserver(state)
	}
}

func agentWaitStatusIsTerminal(status AgentStatus) bool {
	switch status {
	case AgentStatusCompleted, AgentStatusFailed, AgentStatusKilled, AgentStatusNotFound:
		return true
	default:
		return false
	}
}

func (p *AgentPool) notifyAgentTask(state AgentTaskState) {
	if p == nil {
		return
	}
	p.mu.Lock()
	observer := p.taskObserver
	lifecycleObserver := p.lifecycleObserver
	p.mu.Unlock()
	if observer != nil {
		observer(state)
	}
	if lifecycleObserver != nil {
		lifecycleObserver(state)
	}
}

func firstAgentTaskLine(task string) string {
	for _, line := range strings.Split(strings.TrimSpace(task), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (p *AgentPool) job(id string) *agentJob {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.jobs[id]
}

func (p *AgentPool) snapshot(job *agentJob) AgentResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked(job)
}

func (p *AgentPool) snapshotLocked(job *agentJob) AgentResult {
	if job == nil {
		return AgentResult{}
	}
	result := AgentResult{
		ID:             job.id,
		Role:           job.role,
		Status:         job.status,
		Result:         job.result,
		Handoff:        cloneAgentHandoff(job.handoff),
		LastToolName:   job.lastToolName,
		RecentActivity: cloneAgentTaskActivity(job.recentActivity),
	}
	if job.err != nil {
		result.Error = job.err.Error()
	}
	return decorateAgentResultResumeState(result)
}

func decorateAgentResultResumeState(result AgentResult) AgentResult {
	result.ResumeSupported = false
	if agentTerminalCannotResume(result.Status) {
		result.ResumeHint = "Child agent sessions cannot be resumed; spawn a new agent with follow-up context to continue."
	}
	return result
}

func agentTerminalCannotResume(status AgentStatus) bool {
	switch status {
	case AgentStatusCompleted, AgentStatusFailed, AgentStatusKilled, AgentStatusNotFound:
		return true
	default:
		return false
	}
}

func (p *AgentPool) RegisterAgents(agents []AgentDefinition) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.agents == nil {
		p.agents = make(map[string]*AgentDefinition)
	}
	for i := range agents {
		a := agents[i]
		if strings.TrimSpace(a.Name) == "" {
			continue
		}
		p.agents[agentLookupKey(a.Name)] = &a
	}
}

func (p *AgentPool) GetAgent(name string) (*AgentDefinition, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.agents[agentLookupKey(name)]
	return a, ok
}

func (p *AgentPool) AgentNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.agents))
	for _, agent := range p.agents {
		if agent != nil && strings.TrimSpace(agent.Name) != "" {
			names = append(names, strings.TrimSpace(agent.Name))
		}
	}
	sort.Strings(names)
	return names
}

func agentLookupKey(name string) string {
	name = strings.NewReplacer("-", " ", "_", " ").Replace(strings.ToLower(strings.TrimSpace(name)))
	return strings.Join(strings.Fields(name), " ")
}

func DefaultAgentDefinitions() []AgentDefinition {
	readOnlyTools := defaultReadOnlyAgentTools()
	return []AgentDefinition{
		{
			Name:        "repo-auditor",
			Description: "Audit repository architecture, workflows, UX, tests, and gaps against comparable tools.",
			Tools:       readOnlyTools,
			ReadOnly:    true,
			SystemPrompt: strings.Join([]string{
				"You are Forge's repo-auditor agent.",
				"Inspect the repository for architecture, product, UX, testing, workflow, and maintainability evidence.",
				"Return concise findings with concrete file references when available.",
				"Prefer evidence over speculation and call out uncertainty clearly.",
				"Do not create files, run mutation commands, or claim tool access is missing; if asked to write a report, return the report content for the parent agent to save.",
			}, "\n"),
		},
		{
			Name:        "code-reviewer",
			Description: "Review implementation quality, regressions, risks, and missing tests.",
			Tools:       readOnlyTools,
			ReadOnly:    true,
			SystemPrompt: strings.Join([]string{
				"You are Forge's code-reviewer agent.",
				"Prioritize bugs, behavioral regressions, missing tests, and concrete risks.",
				"Report findings first, ordered by severity, with file references when available.",
				"Do not edit files, run mutation commands, or implement fixes; return review findings for the parent agent to act on.",
			}, "\n"),
		},
		{
			Name:        "explorer",
			Description: "Gather codebase evidence quickly across files, symbols, and conventions.",
			Tools:       readOnlyTools,
			ReadOnly:    true,
			SystemPrompt: strings.Join([]string{
				"You are Forge's explorer agent.",
				"Find relevant files, patterns, dependencies, and conventions with minimal speculation.",
				"Return a compact evidence map the parent can use for decisions.",
				"Do not create files, run mutation commands, or claim tool access is missing; return evidence for the parent agent to act on.",
			}, "\n"),
		},
		{
			Name:        "oracle",
			Description: "Analyze hard architecture, debugging, or design questions with extra rigor.",
			Tools:       readOnlyTools,
			ReadOnly:    true,
			SystemPrompt: strings.Join([]string{
				"You are Forge's oracle agent.",
				"Use rigorous reasoning for hard architecture, debugging, or design questions.",
				"Challenge weak assumptions and explain the most likely root cause or tradeoff.",
				"Do not create files, run mutation commands, or claim tool access is missing; return analysis for the parent agent to act on.",
			}, "\n"),
		},
		{
			Name:        "synthesizer",
			Description: "Combine multiple evidence streams into a clear final answer or plan.",
			Tools:       []string{"think"},
			ReadOnly:    true,
			SystemPrompt: strings.Join([]string{
				"You are Forge's synthesizer agent.",
				"Combine evidence from multiple workstreams into a concise, structured result.",
				"Resolve contradictions explicitly and avoid inventing unsupported conclusions.",
				"Use only evidence included in the task prompt; do not inspect repositories or external files.",
				"Do not ask the user to paste files. Do not claim missing filesystem or search tools.",
				"Do not create files, run mutation commands, or claim tool access is missing; return synthesized content for the parent agent to save or act on.",
			}, "\n"),
		},
	}
}

func defaultReadOnlyAgentTools() []string {
	return []string{
		"read_file", "list_dir", "search", "code_search", "glob", "view_image",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols",
		"git_status", "git_diff", "git_log", "git_branch_state", "git_merge_status",
		"tool_help", "think",
	}
}

func MapSpawnRole(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return "default"
	}
	return role
}
