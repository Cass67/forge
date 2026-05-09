package main

import (
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

	mu                 sync.Mutex
	errs               []string
	spawned            bool
	statusToolCalled   bool
	killToolCalled     bool
	secretOutputCalled bool
	secretOutputClean  bool
	secretWriteCalled  bool
	secretWriteClean   bool
	delegatedWrite     bool
	childAuditReadOnly bool
	multipleSpawn      bool
	multipleStatus     bool
	reactiveError      bool
	reactiveRetry      bool
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
		if !agentResultHasStatus(body, "call_audit_wait", "agent-1", "completed") {
			m.recordError("wait_agent result did not report agent-1 completed")
		}
		writeToolCallSSE(w, "call_audit_write", "write_file", map[string]any{"path": "docs/live-audit.md", "content": "Audit finding: fixture ok\n"})
	case strings.Contains(body, "call_audit_write"):
		m.mu.Lock()
		m.delegatedWrite = strings.Contains(toolResultContent(body, "call_audit_write"), "wrote")
		m.mu.Unlock()
		writeTextSSE(w, "REPORT_WRITTEN")
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
		writeTextSSE(w, "SECRET_WRITE_DONE")
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
max_attempts = 1
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
`, baseURL)
	if err := os.WriteFile(filepath.Join(forgeDir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return configHome
}

func initLiveAcceptanceFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Live Acceptance Fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
