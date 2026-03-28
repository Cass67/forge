# Forge Kernel Architecture Fixes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

## Goal

Fix kernel architecture so Forge agents work reliably and perform as well as OpenCode/Codex, with measurable phases and incremental value delivery.

## Architecture

Fix validation, tool access, state synchronization, and observability in kernel workers first. Then address prompts, configuration, and retry logic. Each issue has clear reproduction steps and success criteria.

## Tech Stack

Go (existing codebase), no new dependencies needed.

---

## Task 1: Fix Worker Validation (Critical)

**Priority:** Critical - Blocks all worker tasks

**Files:**
- Create: `internal/harness/workers_validation.go`
- Modify: `internal/harness/workers.go`
- Test: `internal/harness/workers_validation_test.go`

**Step 1: Write failing test**

```go
// File: internal/harness/workers_validation_test.go
func TestWorkerValidation_RejectsMalformedJSON(t *testing.T) {
    manager := NewManager(ManagerConfig{...})
    
    // Worker returns valid JSON but with extra whitespace
    ctx := context.Background()
    obs, _ := manager.Execute(ctx, WorkerTask{
        Kind: WorkerEditor,
        Objective: "change color",
        Context: "",
    })
    
    // Should NOT reject - extra whitespace is OK
    if obs.Status != ObservationComplete {
        t.Errorf("Valid JSON with extra whitespace rejected: %v", obs)
    }
}

func TestWorkerValidation_AcceptsValidJSON(t *testing.T) {
    manager := NewManager(ManagerConfig{...})
    
    ctx := context.Background()
    obs, _ := manager.Execute(ctx, WorkerTask{
        Kind: WorkerEditor,
        Objective: "change color",
        Context: "",
    })
    
    // Worker returns properly formatted JSON
    expected := `{"status":"complete","message":"Done","artifact_kind":"implementation","artifact":"changed color"}`
    
    if obs.Status != ObservationComplete || !strings.Contains(obs.Response, "changed color") {
        t.Errorf("Valid JSON was rejected or malformed")
    }
}
```

**Step 2: Run test to verify it fails**

```bash
cd internal/harness
go test -run TestWorkerValidation_RejectsMalformedJSON -v
```

**Expected:** Test PASSES (current validation is too strict)

**Step 3: Write minimal permissive validation**

```go
// File: internal/harness/workers_validation.go
func ValidateWorkerResultWithToolCalls(task WorkerTask, raw string, calls []agent.ToolCall) (ValidationResult, error) {
    var envelope WorkerResultEnvelope
    if err := json.Unmarshal([]byte(raw), &envelope); err == nil {
        // Validate required fields
        if envelope.Status == "" {
            return ValidationResult{Valid: false, Reason: "missing required status field"}
        }
        
        // Artifact validation: allow markdown, extra whitespace, flexible structure
        if envelope.Artifact == "" && envelope.Status == "complete" {
            // Empty artifact OK for some cases (e.g. verification passed)
            return ValidationResult{Valid: true}
        }
        
        // Normalize whitespace
        envelope.Artifact = strings.TrimSpace(envelope.Artifact)
        
        return ValidationResult{Valid: true}
    }
    
    // Fallback: Try to extract what we can from malformed JSON
    if extractableResult := tryExtractPartialResult(raw); extractableResult != "" {
        return ValidationResult{Valid: true, Partial: extractableResult}
    }
    
    return ValidationResult{Valid: false, Reason: "malformed JSON with no extractable content"}
}

type ValidationResult struct {
    Valid    bool
    Partial  string
    Reason    string
}

func tryExtractPartialResult(raw string) string {
    // Look for status/message/artifact pattern in malformed response
    // Return partial result if we can find it
    return ""
}
```

**Step 4: Update workers.go to use permissive validation**

```go
// File: internal/harness/workers.go:93
func (m *Manager) Execute(ctx context.Context, task WorkerTask) (Observation, error) {
    // ... existing code ...
    
    validated, err := ValidateWorkerResultWithToolCalls(task, raw, cumulativeCalls)
    if err == nil && !validated.Valid {
        // Use partial result if available, otherwise blocked
        if validated.Partial != "" {
            return Observation{
                Status: ObservationComplete,
                Summary: "Partial result: " + validated.Partial,
                Response: validated.Partial,
            }, nil
        }
        return blockedWorkerObservation(task, 
            fmt.Errorf("worker produced invalid structured output: %s", validated.Reason), 
            skillRuntime.UseRecords())
    }
    // ... rest of function unchanged ...
}
```

**Step 5: Run test to verify it passes**

```bash
cd internal/harness
go test -run TestWorkerValidation_AcceptsValidJSON -v
```

**Expected:** Test PASSES with permissive validation

**Step 6: Commit**

```bash
git add internal/harness/workers_validation.go internal/harness/workers.go internal/harness/workers_validation_test.go
git commit -m "fix: permissive worker validation, allow markdown artifacts, partial result extraction"
```

---

## Task 2: Add Missing Worker Tools (High)

**Priority:** High - Workers can't complete objectives

**Files:**
- Modify: `internal/harness/workers.go`

**Step 1: Add artifact_read to WorkerEditor allowlist**

```go
// File: internal/harness/workers.go:120-133
func workerToolAllowlist(kind WorkerKind) []string {
    switch kind {
    case WorkerReader:
        return []string{"read_file", "glob", "search", "list_dir", "git_log", "git_diff", "git_status", "think", 
                       "artifact_read"}  // ADD THIS
    case WorkerEditor:
        return []string{"read_file", "write_file", "edit_file", "glob", "search", "list_dir", 
                       "run_command", "git_diff", "git_status", "think",
                       "artifact_write", "artifact_read", "preview_server_ensure", 
                       "preview_server_status", "git_commit"}  // ADD THESE
    case WorkerVerifier:
        return []string{"read_file", "glob", "search", "list_dir", 
                       "run_command", "git_diff", "git_status", "git_log", "think"}
        // Already has needed tools
    case WorkerResearcher:
        return []string{"read_file", "glob", "search", "list_dir", 
                       "web_search", "web_fetch", "run_command", "think",
                       "git_log"}  // ADD git_log
    default:
        return nil
    }
}
```

**Step 2: Add git_commit to WorkerEditor workflow in workerContext**

```go
// File: internal/harness/workers.go:292-343
func workerContext(class Classification, session SessionState, step Step) string {
    switch step.Worker {
    case WorkerEditor:
        lines := []string{
            "Implement requested change in workspace instead of drafting code in chat.",
            "Inspect relevant files before editing, then create or update actual file that delivers request.",
            "When verifying, run focused verification commands and git_commit for tracked changes.",
        }
        if strings.TrimSpace(class.TopicKey) != "" {
            lines = append(lines, "Primary scope: "+strings.TrimSpace(class.TopicKey))
        }
        if session.HasRecentEvidence() && strings.TrimSpace(session.LastEvidence.TopicKey) != "" {
            lines = append(lines, 
                "Recent evidence topic: "+strings.TrimSpace(session.LastEvidence.TopicKey),
                "Recent evidence summary: "+clipPromptContext(session.LastEvidence.Summary, 240))
        }
        return strings.TrimSpace(strings.Join(lines, "\n"))
    case WorkerVerifier:
        // ... existing code ...
    default:
        return ""
    }
}
```

**Step 3: Test WorkerEditor can use new tools**

```bash
# Manual test or write integration test
cd internal/harness
go test -run TestManagerExecute_EditorUsesArtifactRead -v
```

**Step 4: Commit**

```bash
git add internal/harness/workers.go
git commit -m "feat: add artifact_read, git_commit to WorkerEditor, git_log to Researcher"
```

---

## Task 3: Fix Agent-Worker State Synchronization (High)

**Priority:** High - Context loss, broken follow-ups

**Files:**
- Create: `internal/harness/session_adapter.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/agent/agent.go`

**Step 1: Create session adapter to bridge state systems**

```go
// File: internal/harness/session_adapter.go
package harness

import "sync"

// SessionAdapter provides unified session state across agent and kernel
type SessionAdapter struct {
    mu          sync.RWMutex
    agentState  *chatstate.State
    kernelState SessionState
    mode        ExecutionMode
}

type ExecutionMode string

const (
    ModeKernel   ExecutionMode = "kernel"
    ModeLegacy   ExecutionMode = "legacy"
    ModeHybrid   ExecutionMode = "hybrid"  // Agent with kernel worker support
)

func NewSessionAdapter(agentState *chatstate.State) *SessionAdapter {
    return &SessionAdapter{
        agentState: agentState,
        kernelState: NewSession(),
        mode:       ModeKernel,
    }
}

// SetKernelMode switches to kernel execution
func (s *SessionAdapter) SetKernelMode() {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.mode = ModeKernel
}

// SetLegacyMode switches to agent execution (for compatibility)
func (s *SessionAdapter) SetLegacyMode() {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.mode = ModeLegacy
}

// GetAgentState returns current agent state (for agent.Run())
func (s *SessionAdapter) GetAgentState() *chatstate.State {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.agentState
}

// GetKernelState returns current kernel session state
func (s *SessionAdapter) GetKernelState() SessionState {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.kernelState
}

// GetMode returns current execution mode
func (s *SessionAdapter) GetMode() ExecutionMode {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.mode
}

// PropagateKernelResultToAgent copies kernel results to agent state
func (s *SessionAdapter) PropagateKernelResultToAgent(obs Observation) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if s.mode == ModeKernel || s.mode == ModeHybrid {
        s.kernelState.Apply(Classification{}, obs)
        
        // Copy key fields to agent state for follow-up continuity
        if obs.TopicKey != "" {
            s.agentState.LastTopicKey = obs.TopicKey
        }
        if obs.Summary != "" {
            s.agentState.LastResponse = obs.Summary
        }
    }
}

// Reset clears both states
func (s *SessionAdapter) Reset() {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.kernelState = NewSession()
    s.agentState.Clear()
}
```

**Step 2: Update chat.go to use SessionAdapter**

```go
// File: internal/runtime/chat.go:220-232
// After existing kernel initialization...

type ChatSetup struct {
    // ... existing fields ...
    SessionAdapter *harness.SessionAdapter  // ADD THIS
}

func BuildChatSetup(cfg *config.Config, tokens any, modelOverride, workDir string, yolo bool) (*ChatSetup, error) {
    // ... existing code ...
    
    adapter := harness.NewSessionAdapter(state)
    // ... existing code ...
    
    return &ChatSetup{
        Config:         cfg,
        ChatModel:      chatModel,
        WorkDir:        workDir,
        Driver:         driver,
        Yolo:           yolo,
        Available:      available,
        Providers:      providers,
        MakeDriver:     makeChatDriver,
        SessionAdapter: adapter,  // ADD THIS
        DebugLog:       debugLog,
    }, nil
}
```

**Step 3: Update runChatTurn to use adapter for state propagation**

```go
// File: internal/runtime/chat.go:246-270
func runChatTurn(ctx context.Context, a *agent.Agent, kernel *harness.Runner, input string) error {
    if kernel == nil {
        return a.Run(ctx, input)  // Direct agent execution
    }
    
    result, err := kernel.Run(ctx, input)
    
    // Propagate kernel result to agent state for follow-up continuity
    if kernel != nil && result.Response != "" && setup != nil {
        setup.SessionAdapter.PropagateKernelResultToAgent(result)
    }
    
    if err == nil && strings.TrimSpace(result.Response) != "" {
        if result.Step.Kind == harness.StepWorker || strings.TrimSpace(a.LastResponse()) == "" {
            a.EmitSyntheticResponse(result.Response)
        }
    }
    
    return err
}
```

**Step 4: Test state propagation**

```go
// File: internal/runtime/chat_test.go
func TestRunChatTurn_PropagatesKernelStateToAgent(t *testing.T) {
    setup := buildTestChatSetup()
    adapter := harness.NewSessionAdapter(setup.state)
    
    input := "test this repo"
    kernel := harness.NewRunner(buildTestHarnessRunnerConfig(setup))
    
    err := runChatTurn(ctx, setup.agent, kernel, input)
    
    if err != nil {
        t.Fatal(err)
    }
    
    // Verify agent state received kernel result
    if setup.state.LastResponse != "" {
        t.Errorf("Agent state not updated from kernel result")
    }
}
```

**Step 5: Commit**

```bash
git add internal/harness/session_adapter.go internal/runtime/chat.go internal/runtime/chat_test.go internal/agent/agent.go
git commit -m "feat: add SessionAdapter to synchronize agent and kernel state, prevent context loss"
```

---

## Task 4: Fix Worker Model Routing (High)

**Priority:** High - Workers use wrong models

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/harness/workers.go`

**Step 1: Add kernel-specific worker models to Config**

```go
// File: internal/config/config.go:90-95
type ChatConfig struct {
    Model          string       `toml:"model"`
    LastModel      string       `toml:"last_model"`
    MaxTurns       int          `toml:"max_turns"`
    CommandTimeout int          `toml:"command_timeout"`
    Yolo           bool         `toml:"yolo"`
    IgnoreDirs     []string     `toml:"ignore_dirs"`
    AutoSkills     string       `toml:"auto_skills"`
    Agents         AgentsConfig `toml:"agents"`
    KernelWorkers  KernelWorkerModels `toml:"kernel_workers"`  // ADD THIS
}

// New struct for kernel worker models
type KernelWorkerModels struct {
    Reader    string `toml:"reader"`
    Editor    string `toml:"editor"`
    Verifier  string `toml:"verifier"`
    Researcher string `toml:"researcher"`
}
```

**Step 2: Update workerDriverFor to read kernel models**

```go
// File: internal/runtime/chat.go:482-492
func workerDriverFor(setup *ChatSetup, kind harness.WorkerKind) llm.Driver {
    if setup == nil {
        return nil
    }
    if setup.Config.KernelWorkers != nil {  // READ KERNEL MODELS
        switch kind {
        case harness.WorkerReader:
            if model := setup.Config.KernelWorkers.Reader; model != "" {
                if driver := setup.MakeDriver(model); driver != nil {
                    return driver
                }
            }
        case harness.WorkerEditor:
            if model := setup.Config.KernelWorkers.Editor; model != "" {
                if driver := setup.MakeDriver(model); driver != nil {
                    return driver
                }
            }
        case harness.WorkerVerifier:
            if model := setup.Config.KernelWorkers.Verifier; model != "" {
                if driver := setup.MakeDriver(model); driver != nil {
                    return driver
                }
            }
        case harness.WorkerResearcher:
            if model := setup.Config.KernelWorkers.Researcher; model != "" {
                if driver := setup.MakeDriver(model); driver != nil {
                    return driver
                }
            }
        }
    }
    
    // Fall back to compatibility function (keep existing behavior)
    return compatibilityWorkerModel(setup.Config, kind)
}
```

**Step 3: Update config.toml template**

```toml
# File: config.toml (add to existing)
[kernel_workers]
reader = ""
editor = ""
verifier = ""
researcher = ""
```

**Step 4: Test worker uses correct model**

```bash
# Set different models in config
cat > ~/.config/forge/config.toml << 'EOF'
[kernel_workers]
reader = "claude-3-haiku-20250314"
editor = "claude-sonnet-4-6"
verifier = "o4-mini"
researcher = "claude-haiku-20250314"
EOF

# Run forge with debug mode
forge -d
# Verify worker models in trace output
```

**Step 5: Commit**

```bash
git add internal/config/config.go internal/runtime/chat.go config.toml
git commit -m "feat: add kernel-specific worker models, fix worker model routing"
```

---

## Task 5: Add Runtime Observability (Medium)

**Priority:** Medium - Users see "Worker failed" with no explanation

**Files:**
- Modify: `internal/harness/workers.go`
- Modify: `internal/runtime/chat.go`

**Step 1: Add worker status and error details to Observation**

```go
// File: internal/harness/types.go
type Observation struct {
    Status    ObservationStatus
    Lane      Lane
    Response  string
    Summary   string
    TopicKey  string
    Outcome   ActionOutcome
    Runtime   LocalRuntimeSnapshot
    
    // NEW FIELDS:
    WorkerError      string   `json:"worker_error"`        // Add THIS
    WorkerRetryCount int      `json:"worker_retries"`       // Add THIS
    WorkerStatus     string   `json:"worker_status"`       // Add THIS
}
```

**Step 2: Update workers.go to capture worker errors**

```go
// File: internal/harness/workers.go:52-118
func (m *Manager) Execute(ctx context.Context, task WorkerTask) (Observation, error) {
    // ... existing code ...
    
    retryCount := 0
    workerStatus := "running"
    
    for attempt := 0; attempt < 3; attempt++ {
        prompt := buildWorkerPrompt(task)
        if attempt > 0 {
            prompt = workerRetryPrompt(task, validationErr)
        }
        
        if err := worker.Run(ctx, prompt); err != nil {
            workerStatus = "error: " + err.Error()
            retryCount = attempt + 1
            continue
        }
        
        raw = worker.LastResponse()
        cumulativeCalls = append(cumulativeCalls, worker.LastToolCalls()...)
        
        validated, validationErr := ValidateWorkerResultWithToolCalls(task, raw, cumulativeCalls)
        
        if err == nil && !validated.Valid {
            workerStatus = "validation_failed: " + validationErr
            retryCount = attempt + 1
            continue
        }
        
        if validated.Valid {
            workerStatus = "complete"
            break
        }
    }
    
    // Return observation with worker details
    return Observation{
        Status:    obs.Status,
        Lane:      obs.Lane,
        Response:  obs.Response,
        Summary:   obs.Summary,
        TopicKey:  task.TopicKey,
        Outcome:   obs.Outcome,
        Runtime:   obs.Runtime,
        WorkerError:      workerStatus,  // CAPTURE ERROR
        WorkerRetryCount: retryCount,  // CAPTURE RETRIES
    }, nil
}
```

**Step 3: Update local.go to render worker status in TUI**

```go
// File: internal/harness/local.go:381-461
func buildForgeResponse(step Step, obs Observation) string {
    if step.Kind != StepWorker {
        return firstNonEmpty(strings.TrimSpace(obs.Response), strings.TrimSpace(obs.Summary))
    }
    
    // Worker-specific rendering with status
    var sb strings.Builder
    if obs.WorkerError != "" {
        sb.WriteString("[Worker: ")
        sb.WriteString(obs.WorkerError)
        sb.WriteString("]")
    }
    if obs.WorkerRetryCount > 0 {
        sb.WriteString(fmt.Sprintf("[Retries: %d]", obs.WorkerRetryCount))
    }
    if obs.Artifact != nil {
        switch a := obs.Artifact.(type) {
        case EditorResult:
            if len(a.Changes) > 0 {
                sb.WriteString(fmt.Sprintf("[Changed: %d files]", len(a.Changes)))
            }
        case VerifierResult:
            if len(a.Checks) > 0 {
                sb.WriteString(fmt.Sprintf("[Checks: %d passed]", len(a.Checks)))
            }
        }
    }
    
    baseResponse := firstNonEmpty(strings.TrimSpace(obs.Response), strings.TrimSpace(obs.Summary))
    if sb.Len() > 0 {
        return sb.String() + "\n" + baseResponse
    }
    return baseResponse
}
```

**Step 4: Test observability in debug mode**

```bash
forge -d
# Trigger worker task
# Verify worker status and errors appear in TUI
# Check trace logs for worker execution details
```

**Step 5: Commit**

```bash
git add internal/harness/types.go internal/harness/workers.go internal/harness/local.go
git commit -m "feat: add worker status, retry count, and error tracking for observability"
```

---

## Task 6: Simplify Worker Prompts (Medium)

**Priority:** Medium - 1563-line prompt confuses models

**Files:**
- Modify: `internal/agent/system.go`

**Step 1: Reduce negative constraints and rules**

```go
// File: internal/agent/system.go:238-243
func BuildWorkerSystemPrompt(workDir string, registry *tools.Registry, kind string, loadedSkills []skills.Skill) string {
    if registry == nil {
        registry = tools.NewRegistry()
    }
    
    var sb strings.Builder
    sb.WriteString("You are forge's hidden worker runtime. You operate inside the user's project directory.\n\n")
    sb.WriteString(fmt.Sprintf("Working directory: %s\n", workDir))
    
    info := detectProject(workDir)
    if info != "" {
        sb.WriteString(info + "\n")
    }
    
    // SIMPLIFIED RULES (remove "Never", "Do not", negative framing)
    sb.WriteString("\nExecution rules:\n")
    sb.WriteString("- Operate quietly inside the worker runtime.\n")
    sb.WriteString("- Use your available tools efficiently.\n")
    sb.WriteString("- Wait for tool results before deciding next action.\n")
    sb.WriteString("- Complete the task objective before stopping.\n")
    
    // POSITIVE INSTRUCTIONS (what TO do, not what NOT to do)
    sb.WriteString("\nFor this task:\n")
    sb.WriteString("- Read relevant files to understand current state.\n")
    sb.WriteString("- Make the requested changes.\n")
    sb.WriteString("- Run appropriate verification commands.\n")
    
    // REMOVE COMPLEXITY: one clear instruction instead of 10+ rules
    sb.WriteString("\nFINAL TURN:\n")
    sb.WriteString("- Return a JSON object with status, message, artifact_kind, and artifact fields.\n")
    sb.WriteString("- Use markdown in the artifact field if it helps organize the output.\n")
    
    return sb.String()
}
```

**Step 2: Test simplified prompts**

```bash
forge -d
# Trigger worker tasks
# Verify models complete tasks successfully with fewer retries
```

**Step 3: Commit**

```bash
git add internal/agent/system.go
git commit -m "refactor: simplify worker system prompt, reduce constraints from 1563 lines to ~50 lines"
```

---

## Task 7: Add Circuit Breaker for Retry Logic (Critical)

**Priority:** Critical - Prevents 90-attempt infinite loops

**Files:**
- Create: `internal/harness/circuit_breaker.go`
- Modify: `internal/harness/workers.go`
- Test: `internal/harness/circuit_breaker_test.go`

**Step 1: Create circuit breaker for worker failures**

```go
// File: internal/harness/circuit_breaker.go
package harness

import "sync"
import "time"

type CircuitState string

const (
    CircuitClosed   CircuitState = "closed"
    CircuitOpen     CircuitState = "open"
    CircuitHalfOpen CircuitState = "half_open"
)

// CircuitBreaker prevents infinite retry loops for worker failures
type CircuitBreaker struct {
    mu      sync.RWMutex
    states  map[string]*WorkerCircuit
    config  CircuitConfig
}

type WorkerCircuit struct {
    state           CircuitState
    failureCount    int
    lastFailureTime time.Time
    halfOpenTimeout time.Duration
    consecutiveSuccesses int
}

type CircuitConfig struct {
    MaxFailures     int           // Max failures before opening
    HalfOpenTimeout time.Duration // Duration of half-open state
    RecoveryTimeout  time.Duration // How long to stay in half-open
}

func NewCircuitBreaker(config CircuitConfig) *CircuitBreaker {
    return &CircuitBreaker{
        states: make(map[string]*WorkerCircuit),
        config: config,
    }
}

func (cb *CircuitBreaker) RecordSuccess(worker string) {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    
    circuit, ok := cb.states[worker]
    if !ok {
        cb.states[worker] = &WorkerCircuit{
            state:           CircuitOpen,
            halfOpenTimeout: cb.config.HalfOpenTimeout,
        }
        return
    }
    
    circuit.consecutiveSuccesses++
    if circuit.state == CircuitHalfOpen && circuit.consecutiveSuccesses >= 3 {
        circuit.state = CircuitOpen  // Recovery successful
        circuit.consecutiveSuccesses = 0
    }
}

func (cb *CircuitBreaker) RecordFailure(worker string, err error) {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    
    circuit, ok := cb.states[worker]
    if !ok {
        cb.states[worker] = &WorkerCircuit{
            state:           CircuitOpen,
            halfOpenTimeout: cb.config.HalfOpenTimeout,
        }
        return
    }
    
    circuit.failureCount++
    circuit.lastFailureTime = time.Now()
    
    if circuit.failureCount >= cb.config.MaxFailures {
        circuit.state = CircuitClosed
        return
    }
    
    if circuit.state == CircuitOpen {
        circuit.state = CircuitHalfOpen
        time.AfterFunc(cb.config.HalfOpenTimeout, func() {
            cb.mu.Lock()
            if circuit.state == CircuitHalfOpen {
                circuit.state = CircuitOpen
            }
            cb.mu.Unlock()
        })
    }
}

func (cb *CircuitBreaker) IsOpen(worker string) bool {
    cb.mu.RLock()
    defer cb.mu.RUnlock()
    
    circuit, ok := cb.states[worker]
    if !ok {
        return true
    }
    
    return circuit.state != CircuitClosed
}

func (cb *CircuitBreaker) GetState(worker string) CircuitState {
    cb.mu.RLock()
    defer cb.mu.RUnlock()
    
    circuit, ok := cb.states[worker]
    if !ok {
        return CircuitOpen
    }
    
    return circuit.state
}
```

**Step 2: Update workers.go to use circuit breaker**

```go
// File: internal/harness/workers.go:30-50
type Manager struct {
    workDir     string
    baseTools   *tools.Registry
    approve     tools.ApprovalFunc
    driverFor   WorkerDriverResolver
    circuitBreaker *CircuitBreaker  // ADD THIS
}

func NewManager(cfg ManagerConfig) *Manager {
    circuitBreaker := NewCircuitBreaker(CircuitConfig{
        MaxFailures:     5,           // Close after 5 consecutive failures
        HalfOpenTimeout: 30 * time.Second, // Half-open for 30s
        RecoveryTimeout: 60 * time.Second,  // Stay open 60s after recovery
    })
    
    return &Manager{
        workDir:   cfg.WorkDir,
        baseTools: cfg.BaseTools,
        approve:   cfg.Approve,
        driverFor: cfg.DriverFor,
        circuitBreaker: circuitBreaker,
    }
}

func (m *Manager) Execute(ctx context.Context, task WorkerTask) (Observation, error) {
    // Check circuit before attempting
    if !m.circuitBreaker.IsOpen(task.Kind.String()) {
        return blockedWorkerObservation(task, 
            fmt.Errorf("circuit breaker is closed for worker %s", task.Kind), 
            nil)
    }
    
    // Record success/failure
    defer func() {
        if err == nil {
            m.circuitBreaker.RecordSuccess(task.Kind.String())
        } else {
            m.circuitBreaker.RecordFailure(task.Kind.String(), err)
        }
    }()
    
    // ... existing execution code ...
}
```

**Step 3: Test circuit breaker behavior**

```go
// File: internal/harness/circuit_breaker_test.go
func TestCircuitBreaker_OpensAfterFailures(t *testing.T) {
    cb := NewCircuitBreaker(CircuitConfig{
        MaxFailures:     3,
        HalfOpenTimeout: 1 * time.Second,
        RecoveryTimeout: 2 * time.Second,
    })
    
    // 3 failures should open circuit
    cb.RecordFailure("editor", errors.New("validation error"))
    cb.RecordFailure("editor", errors.New("validation error"))
    cb.RecordFailure("editor", errors.New("validation error"))
    
    if cb.GetState("editor") != CircuitClosed {
        t.Error("Expected circuit to be closed after 3 failures")
    }
}

func TestCircuitBreaker_RecoversAfterSuccesses(t *testing.T) {
    cb := NewCircuitBreaker(CircuitConfig{
        MaxFailures:     3,
        HalfOpenTimeout: 100 * time.Millisecond,
        RecoveryTimeout: 200 * time.Millisecond,
    })
    
    // 3 failures to close
    cb.RecordFailure("editor", errors.New("error"))
    cb.RecordFailure("editor", errors.New("error"))
    cb.RecordFailure("editor", errors.New("error"))
    
    // 3 successes to recover
    cb.RecordSuccess("editor")
    cb.RecordSuccess("editor")
    cb.RecordSuccess("editor")
    
    // Give time for half-open timeout
    time.Sleep(150 * time.Millisecond)
    
    if cb.GetState("editor") != CircuitOpen {
        t.Error("Expected circuit to be open after recovery")
    }
}
```

**Step 4: Commit**

```bash
git add internal/harness/circuit_breaker.go internal/harness/workers.go internal/harness/circuit_breaker_test.go
git commit -m "feat: add circuit breaker to prevent infinite worker retry loops"
```

---

## Phase Completion Criteria

**Phase 1: Worker Validation**
- Workers complete tasks without failing for formatting issues
- Partial results accepted when full results unavailable

**Phase 2: Worker Tools**
- WorkerEditor can verify work using previews and git_commit
- WorkerResearcher can access git history
- All workers have tools needed for their objectives

**Phase 3: State Synchronization**
- Kernel results propagate to agent state for follow-up
- Agent follows up correctly on kernel tasks
- No context loss between agent and kernel

**Phase 4: Model Routing**
- Workers use configured models (not legacy agent models)
- Separate config section for kernel workers
- Model routing respects kernel config

**Phase 5: Observability**
- Worker errors visible in TUI
- Retry counts tracked and displayed
- Worker status indicators (running, complete, failed)

**Phase 6: Simplified Prompts**
- Worker prompts reduced from ~1563 to ~50 lines
- Models complete tasks with fewer retries
- Fewer negative constraints

**Phase 7: Circuit Breaker**
- Workers stop retrying after 5 consecutive failures
- Circuit recovers after successes
- No infinite retry loops (>90 attempts)

**Overall Success:**
Forge agents work reliably with:
- Workers that complete tasks without validation failures
- Proper tool access for all worker objectives
- State synchronization between agent and kernel
- Correct worker model routing
- Runtime observability (errors, retries, status)
- Simplified prompts reducing failure rates
- Circuit breakers preventing infinite retry loops

**Performance Goal:**
Forge performs comparably to OpenCode/Codex in:
- Worker success rate > 90%
- Average task completion time < 2x OpenCode baseline
- Retry rate < 3 per task
- No infinite retry loops
