package react

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type AgentStatus string

const (
	AgentStatusRunning   AgentStatus = "running"
	AgentStatusCompleted AgentStatus = "completed"
	AgentStatusFailed    AgentStatus = "failed"
	AgentStatusTimeout   AgentStatus = "timeout"
	AgentStatusNotFound  AgentStatus = "not_found"
)

type SpawnFunc func(ctx context.Context, role, task string) (string, error)

type AgentResult struct {
	ID     string      `json:"id"`
	Role   string      `json:"role"`
	Status AgentStatus `json:"status"`
	Result string      `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

type agentJob struct {
	id     string
	role   string
	status AgentStatus
	result string
	err    error
	done   chan struct{}
}

type AgentDefinition struct {
	Name         string
	Description  string
	SystemPrompt string
	Model        string
	Fallbacks    []string
	ModelFamily  string
	Tools        []string
}

type AgentPool struct {
	mu     sync.Mutex
	next   int
	jobs   map[string]*agentJob
	spawn  SpawnFunc
	agents map[string]*AgentDefinition
}

func NewAgentPool(spawn SpawnFunc) *AgentPool {
	return &AgentPool{
		jobs:  make(map[string]*agentJob),
		spawn: spawn,
	}
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

	p.mu.Lock()
	p.next++
	id := fmt.Sprintf("agent-%d", p.next)
	job := &agentJob{
		id:     id,
		role:   role,
		status: AgentStatusRunning,
		done:   make(chan struct{}),
	}
	p.jobs[id] = job
	p.mu.Unlock()

	go p.runSpawn(ctx, job, role, task)
	return id, nil
}

func (p *AgentPool) runSpawn(parent context.Context, job *agentJob, role, task string) {
	runCtx := context.Background()
	if parent != nil {
		runCtx = parent
	}
	result, err := p.spawn(runCtx, role, task)

	p.mu.Lock()
	defer p.mu.Unlock()
	job.result = strings.TrimSpace(result)
	job.err = err
	if err != nil {
		job.status = AgentStatusFailed
	} else {
		job.status = AgentStatusCompleted
	}
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
		return AgentResult{ID: id, Status: AgentStatusNotFound}, nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-job.done:
		return p.snapshot(job), nil
	case <-timer.C:
		return AgentResult{ID: id, Role: job.role, Status: AgentStatusTimeout}, nil
	case <-ctx.Done():
		return AgentResult{}, ctx.Err()
	}
}

func (p *AgentPool) job(id string) *agentJob {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.jobs[id]
}

func (p *AgentPool) snapshot(job *agentJob) AgentResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := AgentResult{
		ID:     job.id,
		Role:   job.role,
		Status: job.status,
		Result: job.result,
	}
	if job.err != nil {
		result.Error = job.err.Error()
	}
	return result
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
			SystemPrompt: strings.Join([]string{
				"You are Forge's repo-auditor agent.",
				"Inspect the repository for architecture, product, UX, testing, workflow, and maintainability evidence.",
				"Return concise findings with concrete file references when available.",
				"Prefer evidence over speculation and call out uncertainty clearly.",
			}, "\n"),
		},
		{
			Name:        "code-reviewer",
			Description: "Review implementation quality, regressions, risks, and missing tests.",
			Tools:       readOnlyTools,
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
			SystemPrompt: strings.Join([]string{
				"You are Forge's explorer agent.",
				"Find relevant files, patterns, dependencies, and conventions with minimal speculation.",
				"Return a compact evidence map the parent can use for decisions.",
			}, "\n"),
		},
		{
			Name:        "oracle",
			Description: "Analyze hard architecture, debugging, or design questions with extra rigor.",
			Tools:       readOnlyTools,
			SystemPrompt: strings.Join([]string{
				"You are Forge's oracle agent.",
				"Use rigorous reasoning for hard architecture, debugging, or design questions.",
				"Challenge weak assumptions and explain the most likely root cause or tradeoff.",
			}, "\n"),
		},
		{
			Name:        "synthesizer",
			Description: "Combine multiple evidence streams into a clear final answer or plan.",
			Tools:       readOnlyTools,
			SystemPrompt: strings.Join([]string{
				"You are Forge's synthesizer agent.",
				"Combine evidence from multiple workstreams into a concise, structured result.",
				"Resolve contradictions explicitly and avoid inventing unsupported conclusions.",
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
