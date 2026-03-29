package react

import (
	"context"
	"fmt"
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

type AgentPool struct {
	mu    sync.Mutex
	next  int
	jobs  map[string]*agentJob
	spawn SpawnFunc
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

func MapSpawnRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "default":
		return "default"
	case "worker":
		return "worker"
	case "explorer":
		return "explorer"
	default:
		return "default"
	}
}
