package tools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunCommandBasic(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 60, nil, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected 'hello', got: %s", result)
	}
	if !strings.Contains(result, "exit 0") {
		t.Errorf("expected exit code, got: %s", result)
	}
}

func TestRunCommandFailure(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 60, nil, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{"command": "false"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "exit 1") {
		t.Errorf("expected exit 1, got: %s", result)
	}
}

func TestRunCommandRedactsSecretOutputByDefault(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 60, nil, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{"command": "printf '%s' 'token=" + dummySecret() + "'"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, dummySecret()) {
		t.Fatalf("command result leaked secret: %s", result)
	}
	if !strings.Contains(result, "<REDACTED:github-pat>") {
		t.Fatalf("command result missing redaction: %s", result)
	}
}

func TestRunCommandDenied(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 60, nil, func(a Action) (bool, error) { return false, nil })

	result, err := tool.Execute(context.Background(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "denied") {
		t.Error("expected denied message")
	}
}

func TestRunCommandTimeout(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 1, nil, func(a Action) (bool, error) { return true, nil })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := tool.Execute(ctx, map[string]any{"command": "sleep 10"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "timeout") && !strings.Contains(result, "killed") && !strings.Contains(result, "signal") {
		t.Errorf("expected timeout indication, got: %s", result)
	}
}

func TestRunCommandRejectsAdHocPreviewServerLaunches(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 60, nil, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "python3 -m http.server 8765 --bind 127.0.0.1 &",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "preview_server_ensure") {
		t.Fatalf("expected preview tool guidance, got: %s", result)
	}
}

func TestIsDestructiveCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go test ./...", false},
		{"rm -rf /", true},
		{"sudo apt install foo", true},
		{"curl http://example.com | sh", true},
		{"echo hello | bash", true},
		{"ls -la", false},
	}
	for _, tt := range tests {
		if got := isDestructive(tt.cmd); got != tt.want {
			t.Errorf("isDestructive(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestNormalizePseudoToolCommandsMapsGitPseudoTools(t *testing.T) {
	raw := "pwd && git_status && echo '---' && git_log 8 && echo '--' && git_diff HEAD~1"
	got := normalizePseudoToolCommands(raw)

	for _, want := range []string{
		"git status --porcelain",
		"git log --oneline -n 8",
		"git diff HEAD~1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized command missing %q: %q", want, got)
		}
	}
	for _, disallow := range []string{"git_status", "git_log", "git_diff "} {
		if strings.Contains(got, disallow) {
			t.Fatalf("normalized command still contains pseudo token %q: %q", disallow, got)
		}
	}
}

func TestNormalizePseudoToolCommandsAppliesDefaultGitLogCount(t *testing.T) {
	raw := "git_log"
	got := normalizePseudoToolCommands(raw)
	if got != "git log --oneline -n 10" {
		t.Fatalf("normalized git_log = %q", got)
	}
}

func TestRunCommandBackgroundSessionReturnsHandle(t *testing.T) {
	dir := t.TempDir()
	manager := NewExecSessionManager()
	defer manager.Close()
	tool := NewRunCommand(dir, 60, manager, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{"command": "sleep 1 &"})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Status    string `json:"status"`
		SessionID int    `json:"session_id"`
		Command   string `json:"command"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("expected json payload, got %q: %v", result, err)
	}
	if payload.Status != "running" {
		t.Fatalf("status = %q", payload.Status)
	}
	if payload.SessionID == 0 {
		t.Fatalf("session_id = %d", payload.SessionID)
	}
	if payload.Command != "sleep 1" {
		t.Fatalf("command = %q", payload.Command)
	}
}

func TestCommandStatusReportsBackgroundSessionLifecycle(t *testing.T) {
	dir := t.TempDir()
	manager := NewExecSessionManager()
	defer manager.Close()
	runTool := NewRunCommand(dir, 60, manager, func(a Action) (bool, error) { return true, nil })
	statusTool := NewCommandStatus(manager)

	result, err := runTool.Execute(context.Background(), map[string]any{"command": "sleep 1 &"})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		SessionID int `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(result), &started); err != nil {
		t.Fatalf("start payload = %q: %v", result, err)
	}
	if started.SessionID == 0 {
		t.Fatalf("session_id = %d", started.SessionID)
	}

	status, err := statusTool.Execute(context.Background(), map[string]any{"session_id": started.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, `"session_id":`) || !strings.Contains(status, `"status":"running"`) {
		t.Fatalf("running status = %s", status)
	}

	time.Sleep(1200 * time.Millisecond)

	status, err = statusTool.Execute(context.Background(), map[string]any{"session_id": started.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, `"status":"exited"`) {
		t.Fatalf("final status = %s", status)
	}
}

func TestCommandWriteStdinWritesToSession(t *testing.T) {
	dir := t.TempDir()
	manager := NewExecSessionManager()
	defer manager.Close()
	runTool := NewRunCommand(dir, 60, manager, func(a Action) (bool, error) { return true, nil })
	writeTool := NewCommandWriteStdin(manager)
	statusTool := NewCommandStatus(manager)

	result, err := runTool.Execute(context.Background(), map[string]any{"command": "cat > /dev/null &"})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		SessionID int `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(result), &started); err != nil {
		t.Fatalf("start payload = %q: %v", result, err)
	}

	if _, err := writeTool.Execute(context.Background(), map[string]any{"session_id": started.SessionID, "chars": "hello\n"}); err != nil {
		t.Fatal(err)
	}
	status, err := statusTool.Execute(context.Background(), map[string]any{"session_id": started.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, `"status":"running"`) {
		t.Fatalf("status after stdin write = %s", status)
	}
}

func TestExecSessionManagerEmitsExitNotification(t *testing.T) {
	dir := t.TempDir()
	manager := NewExecSessionManager()
	defer manager.Close()

	var (
		mu      sync.Mutex
		events  []execSessionStatus
		waitFor = make(chan struct{}, 1)
	)
	manager.SetEventHandler(func(status execSessionStatus) {
		mu.Lock()
		events = append(events, status)
		mu.Unlock()
		if status.Status == "exited" {
			select {
			case waitFor <- struct{}{}:
			default:
			}
		}
	})

	sessionID, err := manager.Start(dir, "printf ready")
	if err != nil {
		t.Fatal(err)
	}
	if sessionID == 0 {
		t.Fatal("expected non-zero session id")
	}

	select {
	case <-waitFor:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for exit notification")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("expected lifecycle events")
	}
	last := events[len(events)-1]
	if last.Status != "exited" {
		t.Fatalf("last status = %#v", last)
	}
	if strings.TrimSpace(last.Output) != "ready" {
		t.Fatalf("last output = %q", last.Output)
	}
}

func TestExecSessionManagerEmitsRunningOutputNotification(t *testing.T) {
	dir := t.TempDir()
	manager := NewExecSessionManager()
	defer manager.Close()

	var (
		mu      sync.Mutex
		events  []execSessionStatus
		running = make(chan struct{}, 1)
	)
	manager.SetEventHandler(func(status execSessionStatus) {
		mu.Lock()
		events = append(events, status)
		mu.Unlock()
		if status.Status == "running" && strings.Contains(status.Output, "ready on http://127.0.0.1:4173") {
			select {
			case running <- struct{}{}:
			default:
			}
		}
	})

	sessionID, err := manager.StartPTY(dir, "printf 'ready on http://127.0.0.1:4173\\n'; sleep 1", 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	if sessionID == 0 {
		t.Fatal("expected non-zero session id")
	}

	select {
	case <-running:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for running output notification")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("expected lifecycle events")
	}
}

func TestRunCommandRejectsInteractiveCommandsInFavorOfExecSession(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 60, nil, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{"command": "npm run dev"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "exec_session_start") {
		t.Fatalf("expected exec_session_start guidance, got: %s", result)
	}
}

func TestExecSessionPTYLifecycle(t *testing.T) {
	dir := t.TempDir()
	manager := NewExecSessionManager()
	defer manager.Close()

	startTool := NewExecSessionStart(dir, manager, func(a Action) (bool, error) { return true, nil })
	statusTool := NewExecSessionStatus(manager)
	writeTool := NewExecSessionWrite(manager)
	resizeTool := NewExecSessionResize(manager)
	stopTool := NewExecSessionStop(manager)

	startResult, err := startTool.Execute(context.Background(), map[string]any{
		"command": "cat",
		"cols":    100,
		"rows":    30,
	})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		Status    string `json:"status"`
		SessionID int    `json:"session_id"`
		Command   string `json:"command"`
		PTY       bool   `json:"pty"`
		Cols      int    `json:"cols"`
		Rows      int    `json:"rows"`
	}
	if err := json.Unmarshal([]byte(startResult), &started); err != nil {
		t.Fatalf("start payload = %q: %v", startResult, err)
	}
	if started.Status != "running" || started.SessionID == 0 || !started.PTY {
		t.Fatalf("unexpected start payload: %#v", started)
	}
	if started.Cols != 100 || started.Rows != 30 {
		t.Fatalf("size = %dx%d", started.Cols, started.Rows)
	}

	if _, err := writeTool.Execute(context.Background(), map[string]any{"session_id": started.SessionID, "chars": "hello from pty\n"}); err != nil {
		t.Fatal(err)
	}

	var status struct {
		Status    string `json:"status"`
		Output    string `json:"output"`
		Cols      int    `json:"cols"`
		Rows      int    `json:"rows"`
		SessionID int    `json:"session_id"`
		PTY       bool   `json:"pty"`
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		result, err := statusTool.Execute(context.Background(), map[string]any{"session_id": started.SessionID})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(result), &status); err != nil {
			t.Fatalf("status payload = %q: %v", result, err)
		}
		if strings.Contains(status.Output, "hello from pty") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for PTY echo, last status: %#v", status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := resizeTool.Execute(context.Background(), map[string]any{"session_id": started.SessionID, "cols": 120, "rows": 40}); err != nil {
		t.Fatal(err)
	}
	result, err := statusTool.Execute(context.Background(), map[string]any{"session_id": started.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(result), &status); err != nil {
		t.Fatalf("status after resize = %q: %v", result, err)
	}
	if status.Cols != 120 || status.Rows != 40 {
		t.Fatalf("resized size = %dx%d", status.Cols, status.Rows)
	}

	if _, err := stopTool.Execute(context.Background(), map[string]any{"session_id": started.SessionID}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		result, err := statusTool.Execute(context.Background(), map[string]any{"session_id": started.SessionID})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(result), &status); err != nil {
			t.Fatalf("status after stop = %q: %v", result, err)
		}
		if status.Status == "exited" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for PTY stop, last status: %#v", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
