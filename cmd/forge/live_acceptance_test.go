package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLiveAcceptanceStatusAndCancellationWithLocalProvider(t *testing.T) {
	server := newLiveAcceptanceMock(t)
	defer server.Close()
	bin := buildForgeBinary(t)
	workDir := initLiveAcceptanceFixture(t)
	configHome := writeLiveAcceptanceConfig(t, server.URL())

	output, debugLog := runForgeConsole(t, bin, configHome, workDir, strings.Join([]string{
		`LIVE_STATUS_CANCEL_CHECK: Spawn a repo-auditor child agent with task "child keeps running until canceled". After spawning, do not wait; answer SPAWNED_CHILD.`,
		`LIVE_STATUS_QUERY: What child agents are running? Use agent_status and answer with the child id and status.`,
		`LIVE_CANCEL_QUERY: Cancel the running child agent using kill_agent and answer with the child id and status.`,
		`/quit`,
	}, "\n")+"\n")

	for _, want := range []string{"SPAWNED_CHILD", "agent-1 running", "agent-1 killed"} {
		if !strings.Contains(output, want) {
			t.Fatalf("console output missing %q:\n%s", want, output)
		}
	}
	server.AssertStatusAndCancel(t)
	debugText := readTextFile(t, debugLog)
	for _, want := range []string{`"msg":"chat.agent_lifecycle"`, `"status":"running"`, `"status":"killed"`} {
		if !strings.Contains(debugText, want) {
			t.Fatalf("debug log missing %q", want)
		}
	}
}

func TestLiveAcceptanceHeadlessPromptWithLocalProvider(t *testing.T) {
	server := newLiveAcceptanceMock(t)
	defer server.Close()
	bin := buildForgeBinary(t)
	workDir := initLiveAcceptanceFixture(t)
	configHome := writeLiveAcceptanceConfig(t, server.URL())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-yolo", "-C", workDir, "--model", "mock/mock-model", "-p", "HEADLESS_CHECK say hello")
	cmd.Env = []string{
		"XDG_CONFIG_HOME=" + configHome,
		"HOME=" + filepath.Join(configHome, "home"),
		"MOCK_API_KEY=mock-key",
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + os.TempDir(),
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("headless run failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "OK" {
		t.Fatalf("stdout = %q, want clean final response %q\nstderr:\n%s", got, "OK", stderr.String())
	}
}

func TestLiveAcceptanceDelegatedAuditWritesReportWithLocalProvider(t *testing.T) {
	server := newLiveAcceptanceMock(t)
	defer server.Close()
	bin := buildForgeBinary(t)
	workDir := initLiveAcceptanceFixture(t)
	configHome := writeLiveAcceptanceConfig(t, server.URL())

	output, _ := runForgeConsole(t, bin, configHome, workDir, strings.Join([]string{
		`LIVE_DELEGATED_WRITE_CHECK: Spawn a repo-auditor child agent to audit this fixture, wait for it, then write the report to docs/live-audit.md.`,
		`/quit`,
	}, "\n")+"\n")

	if !strings.Contains(output, "REPORT_WRITTEN") {
		t.Fatalf("console output missing report confirmation:\n%s", output)
	}
	report := readTextFile(t, filepath.Join(workDir, "docs", "live-audit.md"))
	if !strings.Contains(report, "Audit finding: fixture ok") {
		t.Fatalf("report missing child audit content: %q", report)
	}
	server.AssertDelegatedWrite(t)
}

func TestFailureFixtureTermWranglerWouldNotPassContract(t *testing.T) {
	path := filepath.Join("testdata", "failure_threads", "term_wrangler_unknown_tool_dsml.jsonl")
	result := analyzeFailureThreadContract(t, path)

	if result.status != "failed_contract" {
		t.Fatalf("status = %q, want failed_contract; violations=%v", result.status, result.violations)
	}
	for _, want := range []string{"unknown_tool", "raw_tool_markup", "missing_artifact", "provider_child_failure"} {
		if !containsString(result.violations, want) {
			t.Fatalf("violations = %v, want %q", result.violations, want)
		}
	}
}

func TestFailureFixtureAnalyzerAcceptsSuccessfulArtifactWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	writeTextFile(t, path, strings.Join([]string{
		`{"kind":"user_message","message":{"text":"write docs/superpowers/specs/2026-05-23-term-wrangler-design.md"}}`,
		`{"kind":"tool_call","tool_call":{"tool_name":"write_file","tool_call_id":"write-1","args":{"path":"docs/superpowers/specs/2026-05-23-term-wrangler-design.md","content":"# term_wrangler Design\n\n## Backend Detection\n\nUse terminal-native backends with explicit fallback behavior and verification steps."}}}`,
		`{"kind":"tool_result","tool_result":{"tool_call_id":"write-1","text":"wrote docs/superpowers/specs/2026-05-23-term-wrangler-design.md"}}`,
		`{"kind":"assistant_message","message":{"text":"Done. Wrote docs/superpowers/specs/2026-05-23-term-wrangler-design.md with the approved backend design."}}`,
		`{"kind":"turn_complete","turn_complete":{"status":"completed"}}`,
	}, "\n")+"\n")

	result := analyzeFailureThreadContract(t, path)

	if result.status != "satisfied_contract" {
		t.Fatalf("status = %q, want satisfied_contract; violations=%v", result.status, result.violations)
	}
	if len(result.violations) != 0 {
		t.Fatalf("violations = %v, want none", result.violations)
	}
}

func TestFailureFixtureAnalyzerRejectsDoneWithoutArtifactEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	writeTextFile(t, path, strings.Join([]string{
		`{"kind":"user_message","message":{"text":"write docs/superpowers/specs/2026-05-23-term-wrangler-design.md"}}`,
		`{"kind":"assistant_message","message":{"text":"Done. I wrote docs/superpowers/specs/2026-05-23-term-wrangler-design.md."}}`,
		`{"kind":"turn_complete","turn_complete":{"status":"completed"}}`,
	}, "\n")+"\n")

	result := analyzeFailureThreadContract(t, path)

	if result.status != "failed_contract" {
		t.Fatalf("status = %q, want failed_contract; violations=%v", result.status, result.violations)
	}
	if !containsString(result.violations, "missing_artifact") {
		t.Fatalf("violations = %v, want missing_artifact", result.violations)
	}
}

func TestFailureFixtureAnalyzerRejectsFailedWriteFileWithCorrectArgs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	writeTextFile(t, path, strings.Join([]string{
		`{"kind":"turn_contract","turn_contract":{"required_artifacts":[{"path":"docs/superpowers/specs/2026-05-23-term-wrangler-design.md"}]}}`,
		`{"kind":"tool_call","tool_call":{"tool_name":"write_file","tool_call_id":"write-1","args":{"path":"docs/superpowers/specs/2026-05-23-term-wrangler-design.md","content":"# term_wrangler Design\n\n## Backend\n\nUse native terminal backends with clear fallback behavior."}}}`,
		`{"kind":"tool_result","tool_result":{"tool_call_id":"write-1","text":"blocked: refusing to write requested artifact"}}`,
		`{"kind":"assistant_message","message":{"text":"Done. Wrote docs/superpowers/specs/2026-05-23-term-wrangler-design.md."}}`,
		`{"kind":"turn_complete","turn_complete":{"status":"completed"}}`,
	}, "\n")+"\n")

	result := analyzeFailureThreadContract(t, path)

	if result.status != "failed_contract" {
		t.Fatalf("status = %q, want failed_contract; violations=%v", result.status, result.violations)
	}
	if !containsString(result.violations, "missing_artifact") {
		t.Fatalf("violations = %v, want missing_artifact", result.violations)
	}
}

func TestFailureFixtureAnalyzerAcceptsTurnContractWriteEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	writeTextFile(t, path, strings.Join([]string{
		`{"kind":"turn_contract","turn_contract":{"required_artifacts":[{"path":"docs/reports/audit-design.md"}]}}`,
		`{"kind":"tool_call","tool_call":{"tool_name":"artifact_write","tool_call_id":"artifact-1","args":{"path":"docs/reports/audit-design.md","content":"# Audit Design\n\n## backend\n\nUse collected repository evidence to produce a durable audit artifact."}}}`,
		`{"kind":"turn_contract","turn_contract":{"required_artifacts":[{"path":"docs/reports/audit-design.md"}],"evidence":[{"kind":"write","summary":"write: artifact_write docs/reports/audit-design.md"}]}}`,
		`{"kind":"turn_complete","turn_complete":{"status":"completed"}}`,
	}, "\n")+"\n")

	result := analyzeFailureThreadContract(t, path)

	if result.status != "satisfied_contract" {
		t.Fatalf("status = %q, want satisfied_contract; violations=%v", result.status, result.violations)
	}
}

func TestFailureFixtureAnalyzerRejectsBackupPathEvidenceForRequiredArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	writeTextFile(t, path, strings.Join([]string{
		`{"kind":"turn_contract","turn_contract":{"required_artifacts":[{"path":"docs/a.md"}]}}`,
		`{"kind":"tool_call","tool_call":{"tool_name":"write_file","tool_call_id":"write-1","args":{"path":"docs/a.md","content":"# Artifact Design\n\n## Plan\n\nThis content has no successful durable write evidence."}}}`,
		`{"kind":"turn_contract","turn_contract":{"required_artifacts":[{"path":"docs/a.md"}],"evidence":[{"kind":"write","summary":"write: write_file docs/a.md.bak"}]}}`,
		`{"kind":"turn_complete","turn_complete":{"status":"completed"}}`,
	}, "\n")+"\n")

	result := analyzeFailureThreadContract(t, path)

	if result.status != "failed_contract" {
		t.Fatalf("status = %q, want failed_contract; violations=%v", result.status, result.violations)
	}
	if !containsString(result.violations, "missing_artifact") {
		t.Fatalf("violations = %v, want missing_artifact", result.violations)
	}
}

func TestFailureFixtureAnalyzerDerivesArtifactPathFromRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	writeTextFile(t, path, strings.Join([]string{
		`{"kind":"user_message","message":{"text":"write docs/superpowers/specs/2027-01-02-widget-launch-design.md"}}`,
		`{"kind":"tool_call","tool_call":{"tool_name":"write_file","tool_call_id":"write-1","args":{"path":"docs/superpowers/specs/2027-01-02-widget-launch-design.md","content":"# Widget Launch Design\n\n## Plan\n\nShip the widget launch in staged milestones with verification."}}}`,
		`{"kind":"tool_result","tool_result":{"tool_call_id":"write-1","text":"wrote docs/superpowers/specs/2027-01-02-widget-launch-design.md"}}`,
		`{"kind":"turn_complete","turn_complete":{"status":"completed"}}`,
	}, "\n")+"\n")

	result := analyzeFailureThreadContract(t, path)

	if result.status != "satisfied_contract" {
		t.Fatalf("status = %q, want satisfied_contract; violations=%v", result.status, result.violations)
	}
}

func TestFailureFixtureAnalyzerAcceptsStructurallyPlausibleMarkdownCaseVariations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	writeTextFile(t, path, strings.Join([]string{
		`{"kind":"turn_contract","turn_contract":{"required_artifacts":[{"path":"docs/superpowers/specs/2026-05-24-term-wrangler-design.md"}]}}`,
		`{"kind":"tool_call","tool_call":{"tool_name":"edit_file","tool_call_id":"edit-1","args":{"path":"docs/superpowers/specs/2026-05-24-term-wrangler-design.md","content":"# term wrangler design\n\n## backend\n\nthis document describes terminal backend choices and fallback behavior."}}}`,
		`{"kind":"tool_result","tool_result":{"tool_call_id":"edit-1","text":"edited docs/superpowers/specs/2026-05-24-term-wrangler-design.md"}}`,
		`{"kind":"turn_complete","turn_complete":{"status":"completed"}}`,
	}, "\n")+"\n")

	result := analyzeFailureThreadContract(t, path)

	if result.status != "satisfied_contract" {
		t.Fatalf("status = %q, want satisfied_contract; violations=%v", result.status, result.violations)
	}
}

func TestFailureFixtureAnalyzerIgnoresUnrelatedWriteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread.jsonl")
	writeTextFile(t, path, strings.Join([]string{
		`{"kind":"tool_call","tool_call":{"tool_name":"bash"}}`,
		`{"kind":"tool_call","tool_call":{"tool_name":"write_file","args":{"path":"notes.txt"}}}`,
		`{"kind":"tool_result","tool_result":{"text":"error: unknown tool \"bash\". Use one of the tools provided for this turn."}}`,
		`{"kind":"tool_result","tool_result":{"text":"{\"status\":\"failed\"}"}}`,
		`{"kind":"assistant_message","message":{"text":"<｜｜DSML｜｜tool_calls></｜｜DSML｜｜tool_calls>"}}`,
		`{"kind":"turn_complete","turn_complete":{"status":"completed"}}`,
	}, "\n")+"\n")

	result := analyzeFailureThreadContract(t, path)

	if result.status != "failed_contract" {
		t.Fatalf("status = %q, want failed_contract; violations=%v", result.status, result.violations)
	}
	if !containsString(result.violations, "missing_artifact") {
		t.Fatalf("violations = %v, want missing_artifact", result.violations)
	}
}

func TestLiveAcceptanceMultipleAgentsStatusWithLocalProvider(t *testing.T) {
	server := newLiveAcceptanceMock(t)
	defer server.Close()
	bin := buildForgeBinary(t)
	workDir := initLiveAcceptanceFixture(t)
	configHome := writeLiveAcceptanceConfig(t, server.URL())

	output, _ := runForgeConsole(t, bin, configHome, workDir, strings.Join([]string{
		`LIVE_MULTI_AGENT_STATUS_CHECK: Spawn two child agents at the same time: a repo-auditor with task "multi slow child" and an explorer with task "multi fast child". After spawning, do not wait; answer MULTI_SPAWNED.`,
		`LIVE_MULTI_STATUS_QUERY: Use agent_status and report both child agent statuses.`,
		`/quit`,
	}, "\n")+"\n")

	for _, want := range []string{"MULTI_SPAWNED", "agent-1 running", "agent-2 completed"} {
		if !strings.Contains(output, want) {
			t.Fatalf("console output missing %q:\n%s", want, output)
		}
	}
	server.AssertMultipleStatus(t)
}

func TestLiveAcceptanceSecretBoundariesWithLocalProvider(t *testing.T) {
	server := newLiveAcceptanceMock(t)
	defer server.Close()
	bin := buildForgeBinary(t)
	workDir := initLiveAcceptanceFixture(t)
	configHome := writeLiveAcceptanceConfig(t, server.URL())

	output, debugLog := runForgeConsole(t, bin, configHome, workDir, strings.Join([]string{
		`LIVE_SECRET_OUTPUT_CHECK: Use run_command to print the configured dummy token, then answer SECRET_OUTPUT_DONE.`,
		`LIVE_SECRET_WRITE_CHECK: Use write_file to write the configured dummy token to blocked-secret.txt, then answer SECRET_WRITE_DONE.`,
		`/quit`,
	}, "\n")+"\n")

	if strings.Contains(output, server.secret) {
		t.Fatal("console output leaked dummy secret")
	}
	for _, want := range []string{"<REDACTED:generic-token>", "blocked: content matched secret rule generic-token", "SECRET_OUTPUT_DONE", "SECRET_WRITE_DONE"} {
		if !strings.Contains(output, want) {
			t.Fatalf("console output missing %q:\n%s", want, output)
		}
	}
	if _, err := os.Stat(filepath.Join(workDir, "blocked-secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("blocked secret write created file, stat err=%v", err)
	}
	debugText := readTextFile(t, debugLog)
	if strings.Contains(debugText, server.secret) {
		t.Fatal("debug log leaked dummy secret")
	}
	server.AssertSecretChecks(t)
}

func TestLiveAcceptanceDebugLogIncludesRetryAttemptsWithLocalProvider(t *testing.T) {
	server := newLiveAcceptanceMock(t)
	defer server.Close()
	bin := buildForgeBinary(t)
	workDir := initLiveAcceptanceFixture(t)
	configHome := writeLiveAcceptanceConfigWithRetryAttempts(t, server.URL(), 2)

	output, debugLog := runForgeConsole(t, bin, configHome, workDir, strings.Join([]string{
		`LIVE_RETRY_DEBUG_CHECK: answer RETRY_DEBUG_OK after any retry.`,
		`/quit`,
	}, "\n")+"\n")

	if !strings.Contains(output, "RETRY_DEBUG_OK") {
		t.Fatalf("console output missing retry success:\n%s", output)
	}
	debugText := readTextFile(t, debugLog)
	for _, want := range []string{`"msg":"llm.retry_attempt"`, `"msg":"llm.retry_wait"`, `"next_attempt":2`} {
		if !strings.Contains(debugText, want) {
			t.Fatalf("debug log missing %q:\n%s", want, debugText)
		}
	}
	server.AssertRetryDebug(t)
}

func TestLiveAcceptanceManualCompactionContinuesWithLocalProvider(t *testing.T) {
	server := newLiveAcceptanceMock(t)
	defer server.Close()
	bin := buildForgeBinary(t)
	workDir := initLiveAcceptanceFixture(t)
	configHome := writeLiveAcceptanceConfig(t, server.URL())

	output, _ := runForgeConsole(t, bin, configHome, workDir, strings.Join([]string{
		`LIVE_COMPACTION_PRIMER one`,
		`LIVE_COMPACTION_PRIMER two`,
		`/compact recent 1`,
		`LIVE_COMPACTION_CONTINUE: reply COMPACTION_CONTINUED.`,
		`/quit`,
	}, "\n")+"\n")

	for _, want := range []string{"compacted conversation history; preserved recent 1 turns", "COMPACTION_CONTINUED"} {
		if !strings.Contains(output, want) {
			t.Fatalf("console output missing %q:\n%s", want, output)
		}
	}
}

func TestLiveAcceptanceReactiveCompactionRecoversWithLocalProvider(t *testing.T) {
	server := newLiveAcceptanceMock(t)
	defer server.Close()
	bin := buildForgeBinary(t)
	workDir := initLiveAcceptanceFixture(t)
	configHome := writeLiveAcceptanceConfig(t, server.URL())

	input := make([]string, 0, 25)
	for i := range 22 {
		input = append(input, fmt.Sprintf("LIVE_REACTIVE_COMPACTION_PRIMER %02d", i))
	}
	input = append(input,
		`LIVE_REACTIVE_COMPACTION_CHECK: answer REACTIVE_COMPACTION_CONTINUED after any automatic recovery.`,
		`/quit`,
	)
	output, _ := runForgeConsole(t, bin, configHome, workDir, strings.Join(input, "\n")+"\n")

	for _, want := range []string{"react runtime: compacting after context window error", "REACTIVE_COMPACTION_CONTINUED"} {
		if !strings.Contains(output, want) {
			t.Fatalf("console output missing %q:\n%s", want, output)
		}
	}
	server.AssertReactiveCompaction(t)
}

type liveAcceptanceMock struct {
	t            *testing.T
	server       *httptest.Server
	secret       string
	childStarted chan struct{}
	fastDone     chan struct{}
	releaseChild chan struct{}

	mu                          sync.Mutex
	errs                        []string
	spawned                     bool
	statusToolCalled            bool
	killToolCalled              bool
	secretOutputCalled          bool
	secretOutputClean           bool
	secretWriteCalled           bool
	secretWriteClean            bool
	delegatedWrite              bool
	childAuditReadOnly          bool
	multipleSpawn               bool
	multipleStatus              bool
	reactiveError               bool
	reactiveRetry               bool
	comparisonSpawn             bool
	comparisonReadOnly          int
	comparisonWait              bool
	comparison500               bool
	retryDebugRequests          int
	retryDebugSuccess           bool
	scopedDocStarted            bool
	scopedDocWrite              bool
	scopedDocCommit             bool
	scopedDocPush               bool
	scopedDocNoShellGitMutation bool
}

func newLiveAcceptanceMock(t *testing.T) *liveAcceptanceMock {
	t.Helper()
	m := &liveAcceptanceMock{
		t:            t,
		secret:       "TOKEN=" + strings.Repeat("x", 24),
		childStarted: make(chan struct{}),
		fastDone:     make(chan struct{}),
		releaseChild: make(chan struct{}),
	}
	m.server = httptest.NewServer(http.HandlerFunc(m.ServeHTTP))
	return m
}

func (m *liveAcceptanceMock) Close() {
	m.server.Close()
}

func (m *liveAcceptanceMock) URL() string {
	return m.server.URL
}

func (m *liveAcceptanceMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/chat/completions" {
		m.recordError("unexpected path " + r.URL.Path)
		http.NotFound(w, r)
		return
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		m.recordError("read request body: " + err.Error())
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	body := string(bodyBytes)

	switch {
	case strings.Contains(body, "LIVE_RETRY_DEBUG_CHECK"):
		m.mu.Lock()
		m.retryDebugRequests++
		requestCount := m.retryDebugRequests
		// The OpenAI-compatible client retries 500s internally; fail enough HTTP
		// requests for Forge's outer RetryDriver to observe and log a retry.
		if requestCount > 3 {
			m.retryDebugSuccess = true
		}
		m.mu.Unlock()
		if requestCount <= 3 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"debug retry overload","code":500}}`)
			return
		}
		writeTextSSE(w, "RETRY_DEBUG_OK")
	case strings.Contains(body, "LIVE_SCOPED_DOC_COMMIT_PUSH_CHECK") && !strings.Contains(body, "call_scoped_write"):
		m.mu.Lock()
		m.scopedDocStarted = true
		m.mu.Unlock()
		writeToolCallSSE(w, "call_scoped_write", "write_file", map[string]any{
			"path":    "FORGE_VS_CODEX.md",
			"content": "# Forge vs Codex\n\nForge and Codex both help with code tasks.\n",
		})
	case strings.Contains(body, "call_scoped_write") && !strings.Contains(body, "call_scoped_control_write"):
		m.mu.Lock()
		m.scopedDocWrite = strings.Contains(toolResultContent(body, "call_scoped_write"), "wrote")
		m.mu.Unlock()
		writeToolCallSSE(w, "call_scoped_control_write", "write_file", map[string]any{
			"path":    "FORGE_VS_CODEX.md",
			"content": "I've successfully created the commit and pushed it.\n",
		})
	case strings.Contains(body, "call_scoped_control_write") && !strings.Contains(body, "call_scoped_git_add"):
		if !strings.Contains(toolResultContent(body, "call_scoped_control_write"), "blocked") {
			m.recordError("control-plane write to scoped artifact was not blocked")
		}
		writeToolCallSSE(w, "call_scoped_git_add", "run_command", map[string]any{"command": "git add -A"})
	case strings.Contains(body, "call_scoped_git_add") && !strings.Contains(body, "call_scoped_commit"):
		blocked := strings.Contains(toolResultContent(body, "call_scoped_git_add"), "blocked")
		m.mu.Lock()
		m.scopedDocNoShellGitMutation = blocked
		m.mu.Unlock()
		if !blocked {
			m.recordError("shell git mutation was not blocked during scoped transaction")
		}
		writeToolCallSSE(w, "call_scoped_commit", "git_commit", map[string]any{"message": "add Forge vs Codex doc"})
	case strings.Contains(body, "call_scoped_commit") && !strings.Contains(body, "call_scoped_push"):
		m.mu.Lock()
		m.scopedDocCommit = strings.Contains(toolResultContent(body, "call_scoped_commit"), "commit")
		m.mu.Unlock()
		writeToolCallSSE(w, "call_scoped_push", "git_push", map[string]any{})
	case strings.Contains(body, "call_scoped_push"):
		m.mu.Lock()
		m.scopedDocPush = strings.Contains(toolResultContent(body, "call_scoped_push"), "remote contains")
		verified := m.scopedDocCommit && m.scopedDocPush
		m.mu.Unlock()
		if !verified {
			m.recordError("scoped commit/push gates did not pass")
		}
		writeTextSSE(w, "SCOPED_DOC_PUSH_VERIFIED")
	case strings.Contains(body, "child keeps running until canceled") && !strings.Contains(body, "LIVE_STATUS_CANCEL_CHECK"):
		closeOnce(m.childStarted)
		select {
		case <-m.releaseChild:
			writeTextSSE(w, "child released")
		case <-r.Context().Done():
		}
	case strings.Contains(body, "multi slow child") && !strings.Contains(body, "LIVE_MULTI_AGENT_STATUS_CHECK"):
		closeOnce(m.childStarted)
		select {
		case <-m.releaseChild:
			writeTextSSE(w, "multi slow child released")
		case <-r.Context().Done():
		}
	case strings.Contains(body, "multi fast child") && !strings.Contains(body, "LIVE_MULTI_AGENT_STATUS_CHECK"):
		closeOnce(m.fastDone)
		writeTextSSE(w, "multi fast child complete")
	case strings.Contains(body, "LIVE_STATUS_CANCEL_CHECK") && strings.Contains(body, "spawn_agent") && !strings.Contains(body, "call_spawn"):
		m.mu.Lock()
		m.spawned = true
		m.mu.Unlock()
		writeToolCallSSE(w, "call_spawn", "spawn_agent", map[string]any{
			"role":             "repo-auditor",
			"task_description": "child keeps running until canceled",
		})
	case strings.Contains(body, "call_spawn") && !strings.Contains(body, "LIVE_STATUS_QUERY"):
		waitForChannel(m.t, m.childStarted, "child start")
		writeTextSSE(w, "SPAWNED_CHILD")
	case strings.Contains(body, "LIVE_STATUS_QUERY") && !strings.Contains(body, "call_status"):
		m.mu.Lock()
		m.statusToolCalled = true
		m.mu.Unlock()
		writeToolCallSSE(w, "call_status", "agent_status", map[string]any{})
	case strings.Contains(body, "call_status") && !strings.Contains(body, "LIVE_CANCEL_QUERY"):
		if !agentStatusResultHas(body, "call_status", "agent-1", "running") {
			m.recordError("agent_status result did not report agent-1 running")
		}
		writeTextSSE(w, "agent-1 running")
	case strings.Contains(body, "LIVE_CANCEL_QUERY") && !strings.Contains(body, "call_kill"):
		m.mu.Lock()
		m.killToolCalled = true
		m.mu.Unlock()
		writeToolCallSSE(w, "call_kill", "kill_agent", map[string]any{"id": "agent-1"})
	case strings.Contains(body, "call_kill"):
		if !agentResultHasStatus(body, "call_kill", "agent-1", "killed") {
			m.recordError("kill_agent result did not report agent-1 killed")
		}
		closeOnce(m.releaseChild)
		writeTextSSE(w, "agent-1 killed")
	case strings.Contains(body, "LIVE_DELEGATED_WRITE_CHECK") && strings.Contains(body, "spawn_agent") && !strings.Contains(body, "call_audit_spawn"):
		writeToolCallSSE(w, "call_audit_spawn", "spawn_agent", map[string]any{
			"role":             "repo-auditor",
			"task_description": "return audit report content",
		})
	case strings.Contains(body, "return audit report content") && !strings.Contains(body, "LIVE_DELEGATED_WRITE_CHECK"):
		readOnly := !requestToolsContain(body, "write_file", "run_command")
		if !readOnly {
			m.recordError("read-only child audit request included write/run tools")
		}
		m.mu.Lock()
		m.childAuditReadOnly = readOnly
		m.mu.Unlock()
		writeTextSSE(w, "Audit finding: fixture ok")
	case strings.Contains(body, "call_audit_spawn") && !strings.Contains(body, "call_audit_wait"):
		writeToolCallSSE(w, "call_audit_wait", "wait_agent", map[string]any{"id": "agent-1", "timeout_seconds": 5})
	case strings.Contains(body, "call_audit_wait") && !strings.Contains(body, "call_audit_write"):
		// `/quit` is queued in the scripted console input and may cancel the wait
		// context before the child status result is stable. Reaching this branch
		// proves the parent called wait_agent; AssertDelegatedWrite verifies the
		// child request itself ran with read-only tools.
		writeToolCallSSE(w, "call_audit_write", "write_file", map[string]any{"path": "docs/live-audit.md", "content": "Audit finding: fixture ok\n"})
	case strings.Contains(body, "call_audit_write"):
		m.mu.Lock()
		m.delegatedWrite = strings.Contains(toolResultContent(body, "call_audit_write"), "wrote")
		m.mu.Unlock()
		writeTextSSE(w, "REPORT_WRITTEN")
	case strings.Contains(body, "LIVE_COMPARISON_MARKUP_CHECK") && strings.Contains(body, "spawn_agent") && !strings.Contains(body, "call_compare_cci_spawn"):
		m.mu.Lock()
		m.comparisonSpawn = true
		m.mu.Unlock()
		writeToolCallsSSE(w,
			liveToolCall{id: "call_compare_cci_spawn", name: "spawn_agent", args: map[string]any{
				"role":             "cci explorer",
				"task_description": "compare cci repo under ~/git/cci",
			}},
			liveToolCall{id: "call_compare_codex_spawn", name: "spawn_agent", args: map[string]any{
				"role":             "codex explorer",
				"task_description": "compare codex repo under ~/git/codex",
			}},
			liveToolCall{id: "call_compare_opencode_spawn", name: "spawn_agent", args: map[string]any{
				"role":             "opencode explorer",
				"task_description": "compare opencode repo under ~/git/opencode",
			}},
		)
	case strings.Contains(body, "compare cci repo under ~/git/cci") && !strings.Contains(body, "LIVE_COMPARISON_MARKUP_CHECK") && !strings.Contains(body, "call_compare_cci_read"):
		m.recordComparisonChildRequest(body)
		writeToolCallSSE(w, "call_compare_cci_read", "read_file", map[string]any{"path": "~/git/cci/README.md"})
	case strings.Contains(body, "call_compare_cci_read"):
		if !strings.Contains(toolResultContent(body, "call_compare_cci_read"), "CCI fixture") {
			m.recordError("cci child did not read ~/git/cci/README.md")
		}
		writeTextSSE(w, "CCI checkpoints and session recall")
	case strings.Contains(body, "compare codex repo under ~/git/codex") && !strings.Contains(body, "LIVE_COMPARISON_MARKUP_CHECK") && !strings.Contains(body, "call_compare_codex_read"):
		m.recordComparisonChildRequest(body)
		writeToolCallSSE(w, "call_compare_codex_read", "read_file", map[string]any{"path": "~/git/codex/README.md"})
	case strings.Contains(body, "call_compare_codex_read"):
		if !strings.Contains(toolResultContent(body, "call_compare_codex_read"), "Codex fixture") {
			m.recordError("codex child did not read ~/git/codex/README.md")
		}
		writeTextSSE(w, "Codex sandbox and exec policy")
	case strings.Contains(body, "compare opencode repo under ~/git/opencode") && !strings.Contains(body, "LIVE_COMPARISON_MARKUP_CHECK") && !strings.Contains(body, "call_compare_opencode_read"):
		m.recordComparisonChildRequest(body)
		writeToolCallSSE(w, "call_compare_opencode_read", "read_file", map[string]any{"path": "~/git/opencode/README.md"})
	case strings.Contains(body, "call_compare_opencode_read"):
		if !strings.Contains(toolResultContent(body, "call_compare_opencode_read"), "OpenCode fixture") {
			m.recordError("opencode child did not read ~/git/opencode/README.md")
		}
		writeTextSSE(w, "OpenCode undo and revert")
	case strings.Contains(body, "call_compare_cci_spawn") && strings.Contains(body, "call_compare_codex_spawn") && strings.Contains(body, "call_compare_opencode_spawn") && !strings.Contains(body, "call_compare_cci_wait"):
		writeToolCallsSSE(w,
			liveToolCall{id: "call_compare_cci_wait", name: "wait_agent", args: map[string]any{"id": "agent-1", "timeout_seconds": 5}},
			liveToolCall{id: "call_compare_codex_wait", name: "wait_agent", args: map[string]any{"id": "agent-2", "timeout_seconds": 5}},
			liveToolCall{id: "call_compare_opencode_wait", name: "wait_agent", args: map[string]any{"id": "agent-3", "timeout_seconds": 5}},
		)
	case strings.Contains(body, "call_compare_cci_wait") && strings.Contains(body, "call_compare_codex_wait") && strings.Contains(body, "call_compare_opencode_wait"):
		for _, item := range []struct{ callID, id string }{
			{"call_compare_cci_wait", "agent-1"},
			{"call_compare_codex_wait", "agent-2"},
			{"call_compare_opencode_wait", "agent-3"},
		} {
			if !agentResultHasStatus(body, item.callID, item.id, "completed") {
				m.recordError(item.callID + " did not report completed")
			}
		}
		m.mu.Lock()
		m.comparisonWait = true
		m.comparison500 = true
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"server overloaded","code":500}}`)
	case strings.Contains(body, "LIVE_MULTI_AGENT_STATUS_CHECK") && strings.Contains(body, "spawn_agent") && !strings.Contains(body, "call_multi_slow_spawn"):
		m.mu.Lock()
		m.multipleSpawn = true
		m.mu.Unlock()
		writeToolCallsSSE(w,
			liveToolCall{id: "call_multi_slow_spawn", name: "spawn_agent", args: map[string]any{
				"role":             "repo-auditor",
				"task_description": "multi slow child",
			}},
			liveToolCall{id: "call_multi_fast_spawn", name: "spawn_agent", args: map[string]any{
				"role":             "explorer",
				"task_description": "multi fast child",
			}},
		)
	case strings.Contains(body, "call_multi_slow_spawn") && strings.Contains(body, "call_multi_fast_spawn") && !strings.Contains(body, "LIVE_MULTI_STATUS_QUERY"):
		waitForChannel(m.t, m.childStarted, "multi slow child start")
		waitForChannel(m.t, m.fastDone, "multi fast child completion")
		writeTextSSE(w, "MULTI_SPAWNED")
	case strings.Contains(body, "LIVE_MULTI_STATUS_QUERY") && !strings.Contains(body, "call_multi_status"):
		writeToolCallSSE(w, "call_multi_status", "agent_status", map[string]any{})
	case strings.Contains(body, "call_multi_status"):
		running := agentStatusResultHas(body, "call_multi_status", "agent-1", "running")
		completed := agentStatusResultHas(body, "call_multi_status", "agent-2", "completed")
		if !running || !completed {
			m.recordError("agent_status result did not report agent-1 running and agent-2 completed")
		}
		m.mu.Lock()
		m.multipleStatus = running && completed
		m.mu.Unlock()
		closeOnce(m.releaseChild)
		writeTextSSE(w, "agent-1 running; agent-2 completed")
	case strings.Contains(body, "LIVE_SECRET_OUTPUT_CHECK") && !strings.Contains(body, "call_secret_output"):
		m.mu.Lock()
		m.secretOutputCalled = true
		m.mu.Unlock()
		writeToolCallSSE(w, "call_secret_output", "run_command", map[string]any{"command": "printf '%s' '" + m.secret + "'"})
	case strings.Contains(body, "call_secret_output") && !strings.Contains(body, "LIVE_SECRET_WRITE_CHECK"):
		if strings.Contains(body, m.secret) {
			m.recordError("secret output follow-up leaked dummy secret in " + requestLeakLocations(body, m.secret))
		}
		m.mu.Lock()
		m.secretOutputClean = !strings.Contains(body, m.secret) && strings.Contains(body, "REDACTED:generic-token")
		m.mu.Unlock()
		writeTextSSE(w, "SECRET_OUTPUT_DONE")
	case strings.Contains(body, "LIVE_SECRET_WRITE_CHECK") && !strings.Contains(body, "call_secret_write"):
		m.mu.Lock()
		m.secretWriteCalled = true
		m.mu.Unlock()
		writeToolCallSSE(w, "call_secret_write", "write_file", map[string]any{"path": "blocked-secret.txt", "content": m.secret})
	case strings.Contains(body, "call_secret_write"):
		if strings.Contains(body, m.secret) {
			m.recordError("secret write follow-up leaked dummy secret in " + requestLeakLocations(body, m.secret))
		}
		m.mu.Lock()
		m.secretWriteClean = !strings.Contains(body, m.secret) && strings.Contains(body, "blocked: content matched secret rule generic-token")
		m.mu.Unlock()
		writeTextSSE(w, "Secret write was blocked as expected. SECRET_WRITE_DONE")
	case strings.Contains(body, "LIVE_REACTIVE_COMPACTION_CHECK") && strings.Contains(body, "Earlier conversation summary"):
		m.mu.Lock()
		m.reactiveRetry = true
		m.mu.Unlock()
		writeTextSSE(w, "REACTIVE_COMPACTION_CONTINUED")
	case strings.Contains(body, "LIVE_REACTIVE_COMPACTION_CHECK"):
		m.mu.Lock()
		m.reactiveError = true
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"maximum context length exceeded","type":"context_length_exceeded"}}`)
	case strings.Contains(body, "LIVE_REACTIVE_COMPACTION_PRIMER"):
		writeTextSSE(w, "reactive primer acknowledged")
	case strings.Contains(body, "LIVE_COMPACTION_CONTINUE"):
		writeTextSSE(w, "COMPACTION_CONTINUED")
	case strings.Contains(body, "LIVE_COMPACTION_PRIMER"):
		writeTextSSE(w, "primer acknowledged")
	default:
		writeTextSSE(w, "OK")
	}
}

func (m *liveAcceptanceMock) AssertStatusAndCancel(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.errs) > 0 {
		t.Fatalf("mock server errors: %s", strings.Join(m.errs, "; "))
	}
	if !m.spawned || !m.statusToolCalled || !m.killToolCalled {
		t.Fatalf("spawn/status/kill flags = %v/%v/%v", m.spawned, m.statusToolCalled, m.killToolCalled)
	}
}

func (m *liveAcceptanceMock) AssertSecretChecks(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.errs) > 0 {
		t.Fatalf("mock server errors: %s", strings.Join(m.errs, "; "))
	}
	if !m.secretOutputCalled || !m.secretOutputClean {
		t.Fatalf("secret output flags = called:%v clean:%v", m.secretOutputCalled, m.secretOutputClean)
	}
	if !m.secretWriteCalled || !m.secretWriteClean {
		t.Fatalf("secret write flags = called:%v clean:%v", m.secretWriteCalled, m.secretWriteClean)
	}
}

func (m *liveAcceptanceMock) AssertRetryDebug(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.errs) > 0 {
		t.Fatalf("mock server errors: %s", strings.Join(m.errs, "; "))
	}
	if m.retryDebugRequests < 4 || !m.retryDebugSuccess {
		t.Fatalf("retry debug flags = requests:%v success:%v", m.retryDebugRequests, m.retryDebugSuccess)
	}
}

func (m *liveAcceptanceMock) AssertDelegatedWrite(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.errs) > 0 {
		t.Fatalf("mock server errors: %s", strings.Join(m.errs, "; "))
	}
	if !m.delegatedWrite || !m.childAuditReadOnly {
		t.Fatalf("delegated write flags = write:%v childReadOnly:%v", m.delegatedWrite, m.childAuditReadOnly)
	}
}

func (m *liveAcceptanceMock) AssertComparisonMarkup(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.errs) > 0 {
		t.Fatalf("mock server errors: %s", strings.Join(m.errs, "; "))
	}
	if !m.comparisonSpawn || !m.comparisonWait || !m.comparison500 || m.comparisonReadOnly != 3 {
		t.Fatalf("comparison flags = spawn:%v wait:%v server500:%v readOnly:%d", m.comparisonSpawn, m.comparisonWait, m.comparison500, m.comparisonReadOnly)
	}
}

func (m *liveAcceptanceMock) AssertScopedDocCommitPush(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.errs) > 0 {
		t.Fatalf("mock server errors: %s", strings.Join(m.errs, "; "))
	}
	if !m.scopedDocStarted || !m.scopedDocWrite || !m.scopedDocCommit || !m.scopedDocPush || !m.scopedDocNoShellGitMutation {
		t.Fatalf("scoped doc flags = started:%v write:%v commit:%v push:%v noShellGitMutation:%v", m.scopedDocStarted, m.scopedDocWrite, m.scopedDocCommit, m.scopedDocPush, m.scopedDocNoShellGitMutation)
	}
}

func (m *liveAcceptanceMock) AssertMultipleStatus(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.errs) > 0 {
		t.Fatalf("mock server errors: %s", strings.Join(m.errs, "; "))
	}
	if !m.multipleSpawn || !m.multipleStatus {
		t.Fatalf("multi-agent flags = spawn:%v status:%v", m.multipleSpawn, m.multipleStatus)
	}
}

func (m *liveAcceptanceMock) AssertReactiveCompaction(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.errs) > 0 {
		t.Fatalf("mock server errors: %s", strings.Join(m.errs, "; "))
	}
	if !m.reactiveError || !m.reactiveRetry {
		t.Fatalf("reactive compaction flags = error:%v retry:%v", m.reactiveError, m.reactiveRetry)
	}
}

func (m *liveAcceptanceMock) recordError(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errs = append(m.errs, msg)
}

func (m *liveAcceptanceMock) recordComparisonChildRequest(body string) {
	readOnly := !requestToolsContain(body, "write_file", "run_command")
	m.mu.Lock()
	defer m.mu.Unlock()
	if readOnly {
		m.comparisonReadOnly++
		return
	}
	m.errs = append(m.errs, "comparison child request included write/run tools")
}

func writeTextSSE(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	writeSSEData(w, chatChunk(map[string]any{"content": text}, nil))
	writeSSEData(w, chatChunk(map[string]any{}, "stop"))
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

type liveToolCall struct {
	id   string
	name string
	args map[string]any
}

func writeToolCallSSE(w http.ResponseWriter, id, name string, args map[string]any) {
	writeToolCallsSSE(w, liveToolCall{id: id, name: name, args: args})
}

func writeToolCallsSSE(w http.ResponseWriter, calls ...liveToolCall) {
	w.Header().Set("Content-Type", "text/event-stream")
	toolCalls := make([]any, 0, len(calls))
	for i, call := range calls {
		argsJSON, _ := json.Marshal(call.args)
		toolCalls = append(toolCalls, map[string]any{
			"index": i,
			"id":    call.id,
			"type":  "function",
			"function": map[string]any{
				"name":      call.name,
				"arguments": string(argsJSON),
			},
		})
	}
	delta := map[string]any{"tool_calls": toolCalls}
	writeSSEData(w, chatChunk(delta, nil))
	writeSSEData(w, chatChunk(map[string]any{}, "tool_calls"))
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func chatChunk(delta map[string]any, finish any) map[string]any {
	return map[string]any{
		"id":     "chatcmpl-test",
		"object": "chat.completion.chunk",
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         delta,
			"finish_reason": finish,
		}},
	}
}

func writeSSEData(w http.ResponseWriter, payload map[string]any) {
	data, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
}

func requestLeakLocations(body, secret string) string {
	var req struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return "unparsed request"
	}
	var locations []string
	for i, msg := range req.Messages {
		content, _ := json.Marshal(msg.Content)
		if strings.Contains(string(content), secret) {
			locations = append(locations, fmt.Sprintf("message[%d].content(%s)", i, msg.Role))
		}
		for j, call := range msg.ToolCalls {
			if strings.Contains(call.Function.Arguments, secret) {
				locations = append(locations, fmt.Sprintf("message[%d].tool_calls[%d].%s", i, j, call.Function.Name))
			}
		}
	}
	if len(locations) == 0 {
		return "unknown request field"
	}
	return strings.Join(locations, ", ")
}

func requestToolsContain(body string, names ...string) bool {
	var req struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return false
	}
	want := make(map[string]struct{}, len(names))
	for _, name := range names {
		want[name] = struct{}{}
	}
	for _, tool := range req.Tools {
		if _, ok := want[tool.Function.Name]; ok {
			return true
		}
	}
	return false
}

func toolResultContent(body, callID string) string {
	var req struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return ""
	}
	for _, msg := range req.Messages {
		if msg.Role == "tool" && msg.ToolCallID == callID {
			return msg.Content
		}
	}
	return ""
}

func agentStatusResultHas(body, callID, id, status string) bool {
	var payload struct {
		Agents []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(toolResultContent(body, callID)), &payload); err != nil {
		return false
	}
	for _, agent := range payload.Agents {
		if agent.ID == id && agent.Status == status {
			return true
		}
	}
	return false
}

func agentResultHasStatus(body, callID, id, status string) bool {
	var payload struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(toolResultContent(body, callID)), &payload); err != nil {
		return false
	}
	return payload.ID == id && payload.Status == status
}

func buildForgeBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "forge")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/forge")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build forge: %v\n%s", err, out)
	}
	return bin
}

func runForgeConsole(t *testing.T, bin, configHome, workDir, input string) (string, string) {
	t.Helper()
	debugLog := filepath.Join(t.TempDir(), "forge-debug.jsonl")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-yolo", "-d", "-debug-file", debugLog, "-C", workDir, "--model", "mock/mock-model")
	cmd.Env = []string{
		"FORGE_CHAT_CONSOLE=1",
		"XDG_CONFIG_HOME=" + configHome,
		"HOME=" + filepath.Join(configHome, "home"),
		"MOCK_API_KEY=mock-key",
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + os.TempDir(),
	}
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("forge command timed out; output:\n%s", out)
	}
	if err != nil {
		t.Fatalf("forge command failed: %v\n%s", err, out)
	}
	return string(out), debugLog
}

func writeLiveAcceptanceConfig(t *testing.T, baseURL string) string {
	t.Helper()
	return writeLiveAcceptanceConfigWithRetryAttempts(t, baseURL, 1)
}

func writeLiveAcceptanceConfigWithRetryAttempts(t *testing.T, baseURL string, maxAttempts int) string {
	t.Helper()
	configHome := t.TempDir()
	forgeDir := filepath.Join(configHome, "forge")
	if err := os.MkdirAll(forgeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(`live_compat_models = false

[chat]
model = "mock/mock-model"
auto_skills = "off"

[retry]
max_attempts = %d
initial_wait_ms = 10
max_wait_ms = 10
timeout_seconds = 10

[resilience]
stream_idle_timeout_ms = 5000

[model_providers.mock]
name = "Local Mock"
base_url = %q
wire_api = "chat"
default_model = "mock-model"
models = ["mock-model"]
`, maxAttempts, baseURL)
	if err := os.WriteFile(filepath.Join(forgeDir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return configHome
}

func initLiveAcceptanceFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTextFile(t, filepath.Join(dir, "README.md"), "# Live Acceptance Fixture\n")
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	return dir
}

func initLiveAcceptanceBareRemote(t *testing.T, workDir string) {
	t.Helper()
	runLiveAcceptanceGit(t, workDir, "branch", "-M", "main")
	remote := filepath.Join(t.TempDir(), "origin.git")
	runLiveAcceptanceGit(t, t.TempDir(), "init", "--bare", remote)
	runLiveAcceptanceGit(t, workDir, "remote", "add", "origin", remote)
}

func initLiveAcceptanceComparisonRepos(t *testing.T, configHome string) {
	t.Helper()
	for name, content := range map[string]string{
		"cci":      "# CCI fixture\nFeature: checkpoints and session recall\n",
		"codex":    "# Codex fixture\nFeature: sandbox and exec policy\n",
		"opencode": "# OpenCode fixture\nFeature: undo and revert\n",
	} {
		dir := filepath.Join(configHome, "home", "git", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeTextFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return runLiveAcceptanceGit(t, dir, args...)
}

func runLiveAcceptanceGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func waitForChannel(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func closeOnce(ch chan struct{}) {
	defer func() { _ = recover() }()
	close(ch)
}

type failureThreadContractResult struct {
	status     string
	violations []string
}

type failureThreadWriteCall struct {
	toolName string
	path     string
	content  string
}

func analyzeFailureThreadContract(t *testing.T, path string) failureThreadContractResult {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Fatalf("close fixture: %v", err)
		}
	}()

	type threadEvent struct {
		Kind     string `json:"kind"`
		ToolCall struct {
			ToolCallID string `json:"tool_call_id"`
			ToolName   string `json:"tool_name"`
			Args       struct {
				Path    string `json:"path"`
				File    string `json:"file"`
				Content string `json:"content"`
				Patch   string `json:"patch"`
			} `json:"args"`
		} `json:"tool_call"`
		ToolResult struct {
			ToolCallID string `json:"tool_call_id"`
			Text       string `json:"text"`
		} `json:"tool_result"`
		Message struct {
			Text string `json:"text"`
		} `json:"message"`
		TurnContract struct {
			RequiredArtifacts []struct {
				Path string `json:"path"`
			} `json:"required_artifacts"`
			Evidence []struct {
				Kind    string `json:"kind"`
				Summary string `json:"summary"`
			} `json:"evidence"`
			Gates []struct {
				Name     string `json:"name"`
				Status   string `json:"status"`
				Evidence string `json:"evidence"`
			} `json:"gates"`
		} `json:"turn_contract"`
		TurnComplete struct {
			Status string `json:"status"`
		} `json:"turn_complete"`
	}

	var (
		bashToolCalled       bool
		unknownBashTool      bool
		childFailed          bool
		rawToolMarkup        bool
		finalStatus          string
		userTexts            []string
		requiredPaths        []string
		writeCalls           = make(map[string]failureThreadWriteCall)
		artifactContent      = make(map[string]string)
		successfulWritePaths = make(map[string]bool)
		contractWritePaths   = make(map[string]bool)
	)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 4*1024*1024)
	for scanner.Scan() {
		var event threadEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("parse fixture JSONL: %v", err)
		}
		switch event.Kind {
		case "user_message":
			userTexts = append(userTexts, event.Message.Text)
		case "tool_call":
			if event.ToolCall.ToolName == "bash" {
				bashToolCalled = true
			}
			if isArtifactWriteTool(event.ToolCall.ToolName) {
				for _, candidatePath := range artifactWriteToolPaths(event.ToolCall.ToolName, event.ToolCall.Args.Path, event.ToolCall.Args.File, event.ToolCall.Args.Patch) {
					candidate := failureThreadWriteCall{toolName: event.ToolCall.ToolName, path: candidatePath, content: artifactWriteToolContent(event.ToolCall.ToolName, event.ToolCall.Args.Content, event.ToolCall.Args.Patch)}
					if event.ToolCall.ToolCallID != "" {
						writeCalls[event.ToolCall.ToolCallID] = candidate
					}
					if candidate.content != "" {
						artifactContent[normalizeFailureArtifactPath(candidatePath)] = candidate.content
					}
				}
			}
		case "tool_result":
			if bashToolCalled && strings.Contains(event.ToolResult.Text, `unknown tool "bash"`) {
				unknownBashTool = true
			}
			if strings.Contains(event.ToolResult.Text, `"status":"failed"`) || strings.Contains(event.ToolResult.Text, `"status": "failed"`) {
				childFailed = true
			}
			if call, ok := writeCalls[event.ToolResult.ToolCallID]; ok && !artifactWriteResultFailed(event.ToolResult.Text) {
				successfulWritePaths[normalizeFailureArtifactPath(call.path)] = true
			}
		case "assistant_message":
			if strings.Contains(event.Message.Text, "DSML") && strings.Contains(event.Message.Text, "tool_calls") {
				rawToolMarkup = true
			}
		case "turn_contract":
			for _, artifact := range event.TurnContract.RequiredArtifacts {
				requiredPaths = appendFailureArtifactPath(requiredPaths, artifact.Path)
			}
			for _, evidence := range event.TurnContract.Evidence {
				if evidence.Kind != "write" {
					continue
				}
				for _, evidencePath := range failureArtifactEvidencePaths(evidence.Summary) {
					contractWritePaths[normalizeFailureArtifactPath(evidencePath)] = true
				}
			}
			for _, gate := range event.TurnContract.Gates {
				if strings.EqualFold(gate.Status, "passed") {
					for _, evidencePath := range failureArtifactEvidencePaths(gate.Evidence) {
						contractWritePaths[normalizeFailureArtifactPath(evidencePath)] = true
					}
				}
			}
		case "turn_complete":
			finalStatus = event.TurnComplete.Status
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read fixture JSONL: %v", err)
	}

	requiredPaths = append(requiredPaths, requiredArtifactPathsFromUserText(strings.Join(userTexts, "\n"))...)
	artifactWritten := false
	for _, requiredPath := range requiredPaths {
		normalizedPath := normalizeFailureArtifactPath(requiredPath)
		if normalizedPath == "" {
			continue
		}
		if !isPlausibleMarkdownArtifact(artifactContent[normalizedPath]) {
			continue
		}
		if successfulWritePaths[normalizedPath] || contractWritePaths[normalizedPath] {
			artifactWritten = true
			break
		}
	}

	result := failureThreadContractResult{status: finalStatus}
	if unknownBashTool {
		result.violations = append(result.violations, "unknown_tool")
	}
	if rawToolMarkup {
		result.violations = append(result.violations, "raw_tool_markup")
	}
	if childFailed {
		result.violations = append(result.violations, "provider_child_failure")
	}
	if finalStatus == "completed" && !artifactWritten {
		result.violations = append(result.violations, "missing_artifact")
	}
	if finalStatus == "completed" && len(result.violations) == 0 && artifactWritten {
		result.status = "satisfied_contract"
	}
	if finalStatus == "completed" && len(result.violations) > 0 {
		result.status = "failed_contract"
	}
	return result
}

func appendFailureArtifactPath(paths []string, path string) []string {
	path = normalizeFailureArtifactPath(path)
	if path == "" || containsString(paths, path) {
		return paths
	}
	return append(paths, path)
}

func normalizeFailureArtifactPath(path string) string {
	path = strings.Trim(strings.TrimSpace(path), "`'\".,:;)")
	path = strings.TrimPrefix(path, "./")
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func requiredArtifactPathsFromUserText(text string) []string {
	var paths []string
	for _, token := range strings.Fields(text) {
		for _, candidate := range strings.Split(token, ",") {
			candidate = strings.Trim(strings.TrimSpace(candidate), "`'\".,:;)]}")
			if strings.HasSuffix(candidate, ".md") {
				paths = appendFailureArtifactPath(paths, candidate)
			}
		}
	}
	return paths
}

func isArtifactWriteTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "write_file", "edit_file", "apply_patch", "artifact_write":
		return true
	default:
		return false
	}
}

func artifactWriteToolPaths(toolName, path, file, patch string) []string {
	switch strings.TrimSpace(toolName) {
	case "write_file", "edit_file", "artifact_write":
		var paths []string
		paths = appendFailureArtifactPath(paths, path)
		paths = appendFailureArtifactPath(paths, file)
		return paths
	case "apply_patch":
		return artifactPathsFromPatch(patch)
	default:
		return nil
	}
}

func artifactWriteToolContent(toolName, content, patch string) string {
	if strings.TrimSpace(toolName) == "apply_patch" {
		return addedMarkdownContentFromPatch(patch)
	}
	return content
}

func artifactPathsFromPatch(patch string) []string {
	var paths []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "+++ b/"} {
			if strings.HasPrefix(line, prefix) {
				paths = appendFailureArtifactPath(paths, strings.TrimPrefix(line, prefix))
			}
		}
	}
	return paths
}

func addedMarkdownContentFromPatch(patch string) string {
	var lines []string
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			lines = append(lines, strings.TrimPrefix(line, "+"))
		}
	}
	return strings.Join(lines, "\n")
}

func artifactWriteResultFailed(result string) bool {
	lower := strings.ToLower(strings.TrimSpace(result))
	return lower == "" || strings.HasPrefix(lower, "error") || strings.HasPrefix(lower, "blocked") || strings.HasPrefix(lower, "failed") || strings.Contains(lower, " failed") || strings.Contains(lower, "denied")
}

func failureArtifactEvidencePaths(summary string) []string {
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	if paths := requiredArtifactPathsFromUserText(summary); len(paths) > 0 {
		return paths
	}
	idx := strings.Index(summary, ":")
	if idx < 0 {
		return nil
	}
	fields := strings.Fields(strings.TrimSpace(summary[idx+1:]))
	if len(fields) < 2 {
		return nil
	}
	var paths []string
	for _, path := range strings.Split(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(summary[idx+1:]), fields[0])), ",") {
		paths = appendFailureArtifactPath(paths, path)
	}
	return paths
}

func isPlausibleMarkdownArtifact(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" || !strings.Contains(content, "#") {
		return false
	}
	lines := strings.Split(content, "\n")
	hasHeading := false
	hasSection := false
	words := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			hasHeading = true
		}
		if strings.HasPrefix(trimmed, "## ") {
			hasSection = true
		}
		words += len(strings.Fields(trimmed))
	}
	return hasHeading && hasSection && words >= 10
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
