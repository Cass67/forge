package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

const execSessionOutputLimit = 64 * 1024

type ExecSessionManager struct {
	mu       sync.Mutex
	nextID   int
	sessions map[int]*execSession
	onEvent  func(ExecSessionStatus)
}

type execSession struct {
	id        int
	command   string
	workDir   string
	startedAt time.Time

	mu       sync.Mutex
	cmd      *exec.Cmd
	ptyFile  *os.File
	output   bytes.Buffer
	done     bool
	exitCode int
	ptyMode  bool
	cols     int
	rows     int
	onEvent  func(ExecSessionStatus)
	lastEmit time.Time
}

type execSessionStatus struct {
	Status    string `json:"status"`
	SessionID int    `json:"session_id"`
	Command   string `json:"command"`
	Output    string `json:"output,omitempty"`
	ExitCode  int    `json:"exit_code,omitempty"`
	PTY       bool   `json:"pty,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
	// Next names the tool that consumes SessionID. A bare id in a payload is an
	// invitation to guess: observed a model try wait_agent and then read_output
	// with this id before reaching command_status, then fall back to sleeping.
	Next string `json:"next,omitempty"`
}

// nextForSession tells the caller how to follow up on a background command.
func nextForSession(id int) string {
	return fmt.Sprintf(
		"still running: poll with command_status {\"session_id\": %d}. "+
			"Send input with command_write_stdin, stop it with command_stop. "+
			"Do not sleep to wait, and do not pass this id to read_output or wait_agent.",
		id)
}

type ExecSessionStatus = execSessionStatus

func NewExecSessionManager() *ExecSessionManager {
	return &ExecSessionManager{
		nextID:   1,
		sessions: make(map[int]*execSession),
	}
}

func (m *ExecSessionManager) Start(workDir, command string) (int, error) {
	return m.start(workDir, command, 80, 24, true)
}

func (m *ExecSessionManager) StartPTY(workDir, command string, cols, rows int) (int, error) {
	return m.start(workDir, command, cols, rows, true)
}

func (m *ExecSessionManager) start(workDir, command string, cols, rows int, ptyMode bool) (int, error) {
	if m == nil {
		return 0, fmt.Errorf("exec session manager is nil")
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return 0, fmt.Errorf("command is required")
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	cmd := exec.Command("sh", "-c", command)
	if fn := CurrentSandboxArgv(); fn != nil {
		argv, handled, err := fn(workDir, command, ptyMode)
		if err != nil {
			return 0, fmt.Errorf("sandbox session: %w", err)
		}
		if handled && len(argv) > 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		}
	}
	cmd.Dir = workDir
	var (
		stream io.Reader
		ptmx   *os.File
		err    error
	)
	if ptyMode {
		ptmx, err = pty.StartWithSize(cmd, &pty.Winsize{
			Cols: uint16(cols),
			Rows: uint16(rows),
		})
		if err != nil {
			return 0, err
		}
		stream = ptmx
	} else {
		stdout, pipeErr := cmd.StdoutPipe()
		if pipeErr != nil {
			return 0, pipeErr
		}
		stderr, pipeErr := cmd.StderrPipe()
		if pipeErr != nil {
			return 0, pipeErr
		}
		if _, pipeErr = cmd.StdinPipe(); pipeErr != nil {
			return 0, pipeErr
		}
		if pipeErr = cmd.Start(); pipeErr != nil {
			return 0, pipeErr
		}
		stream = io.MultiReader(stdout, stderr)
	}
	if !ptyMode && err != nil {
		return 0, err
	}

	m.mu.Lock()
	id := m.nextID
	m.nextID++
	sess := &execSession{
		id:        id,
		command:   command,
		workDir:   workDir,
		startedAt: time.Now(),
		cmd:       cmd,
		ptyFile:   ptmx,
		ptyMode:   ptyMode,
		cols:      cols,
		rows:      rows,
		onEvent:   m.onEvent,
	}
	m.sessions[id] = sess
	m.mu.Unlock()

	go sess.capture(stream)
	go sess.wait()

	return id, nil
}

func (m *ExecSessionManager) SetEventHandler(fn func(ExecSessionStatus)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEvent = fn
}

func (m *ExecSessionManager) Status(id int) (string, error) {
	if m == nil {
		return "", fmt.Errorf("exec session manager is nil")
	}
	m.mu.Lock()
	sess := m.sessions[id]
	m.mu.Unlock()
	if sess == nil {
		return "", fmt.Errorf("unknown session %d", id)
	}
	payload := sess.snapshot()
	// Repeat the routing hint while it is still running: a model polling a
	// long job re-reads this payload, not the one that started it.
	if payload.Status == "running" {
		payload.Next = nextForSession(id)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *ExecSessionManager) Write(id int, chars string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("exec session manager is nil")
	}
	m.mu.Lock()
	sess := m.sessions[id]
	m.mu.Unlock()
	if sess == nil {
		return "", fmt.Errorf("unknown session %d", id)
	}
	return sess.write(chars)
}

func (m *ExecSessionManager) Resize(id, cols, rows int) (string, error) {
	if m == nil {
		return "", fmt.Errorf("exec session manager is nil")
	}
	m.mu.Lock()
	sess := m.sessions[id]
	m.mu.Unlock()
	if sess == nil {
		return "", fmt.Errorf("unknown session %d", id)
	}
	return sess.resize(cols, rows)
}

func (m *ExecSessionManager) Stop(id int) (string, error) {
	if m == nil {
		return "", fmt.Errorf("exec session manager is nil")
	}
	m.mu.Lock()
	sess := m.sessions[id]
	m.mu.Unlock()
	if sess == nil {
		return "", fmt.Errorf("unknown session %d", id)
	}
	return sess.stop()
}

func (m *ExecSessionManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	sessions := make([]*execSession, 0, len(m.sessions))
	for _, sess := range m.sessions {
		sessions = append(sessions, sess)
	}
	m.mu.Unlock()
	for _, sess := range sessions {
		sess.kill()
	}
}

func (s *execSession) capture(r io.Reader) {
	if s == nil || r == nil {
		return
	}
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.mu.Lock()
			remaining := execSessionOutputLimit - s.output.Len()
			if remaining > 0 {
				if n > remaining {
					n = remaining
				}
				s.output.Write(buf[:n])
			}
			status, notify := s.maybeSnapshotForOutputLocked(buf[:n])
			s.mu.Unlock()
			if notify {
				s.notify(status)
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *execSession) wait() {
	if s == nil || s.cmd == nil {
		return
	}
	err := s.cmd.Wait()
	s.mu.Lock()
	s.done = true
	if err == nil {
		s.exitCode = 0
	} else if exitErr, ok := err.(*exec.ExitError); ok {
		s.exitCode = exitErr.ExitCode()
	} else {
		s.exitCode = 1
	}
	status := s.snapshotLocked()
	s.mu.Unlock()
	if s.ptyFile != nil {
		_ = s.ptyFile.Close()
	}
	s.notify(status)
}

func (s *execSession) kill() {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done {
		return
	}
	_ = s.cmd.Process.Kill()
}

func (s *execSession) notify(status ExecSessionStatus) {
	if s == nil || s.onEvent == nil {
		return
	}
	s.onEvent(status)
}

func (s *execSession) maybeSnapshotForOutputLocked(chunk []byte) (ExecSessionStatus, bool) {
	if s == nil || s.onEvent == nil || s.done {
		return ExecSessionStatus{}, false
	}
	text := string(chunk)
	now := time.Now()
	if !strings.Contains(text, "\n") && now.Sub(s.lastEmit) < 200*time.Millisecond {
		return ExecSessionStatus{}, false
	}
	s.lastEmit = now
	return s.snapshotLocked(), true
}

func (s *execSession) write(chars string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return "", fmt.Errorf("session %d has already exited", s.id)
	}
	if s.ptyFile == nil {
		return "", fmt.Errorf("session %d does not accept input", s.id)
	}
	if _, err := io.WriteString(s.ptyFile, chars); err != nil {
		return "", err
	}
	payload, err := json.Marshal(s.snapshotLocked())
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (s *execSession) resize(cols, rows int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return "", fmt.Errorf("session %d has already exited", s.id)
	}
	if s.ptyFile == nil {
		return "", fmt.Errorf("session %d does not support resize", s.id)
	}
	if cols <= 0 {
		cols = s.cols
	}
	if rows <= 0 {
		rows = s.rows
	}
	if err := pty.Setsize(s.ptyFile, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		return "", err
	}
	s.cols = cols
	s.rows = rows
	payload, err := json.Marshal(s.snapshotLocked())
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (s *execSession) stop() (string, error) {
	s.kill()
	payload, err := json.Marshal(s.snapshot())
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (s *execSession) snapshot() execSessionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *execSession) snapshotLocked() execSessionStatus {
	status := "running"
	if s.done {
		status = "exited"
	}
	return execSessionStatus{
		Status:    status,
		SessionID: s.id,
		Command:   s.command,
		Output:    strings.TrimSpace(s.output.String()),
		ExitCode:  s.exitCode,
		PTY:       s.ptyMode,
		Cols:      s.cols,
		Rows:      s.rows,
	}
}

func NewCommandStatus(manager *ExecSessionManager) Tool {
	tool := NewExecSessionStatus(manager)
	tool.Name = "command_status"
	tool.Description = "Check the status of a background command session."
	return tool
}

func NewExecSessionStatus(manager *ExecSessionManager) Tool {
	return Tool{
		Name:        "exec_session_status",
		Description: "Check the status of an interactive exec session.",
		Parameters: []ParameterDef{
			{Name: "session_id", Type: "int", Description: "exec session id", Required: true},
		},
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			id, _ := args["session_id"].(int)
			if id == 0 {
				if raw, ok := args["session_id"].(float64); ok {
					id = int(raw)
				}
			}
			return manager.Status(id)
		},
	}
}

func NewCommandWriteStdin(manager *ExecSessionManager) Tool {
	tool := NewExecSessionWrite(manager)
	tool.Name = "command_write_stdin"
	tool.Description = "Write input to a background command session."
	return tool
}

func NewExecSessionWrite(manager *ExecSessionManager) Tool {
	return Tool{
		Name:        "exec_session_write",
		Description: "Write input to an interactive exec session.",
		Parameters: []ParameterDef{
			{Name: "session_id", Type: "int", Description: "exec session id", Required: true},
			{Name: "chars", Type: "string", Description: "text to write to the session", Required: true},
		},
		AutoApprove: true,
		Concurrency: ToolConcurrencySerial,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			id, _ := args["session_id"].(int)
			if id == 0 {
				if raw, ok := args["session_id"].(float64); ok {
					id = int(raw)
				}
			}
			chars, _ := args["chars"].(string)
			return manager.Write(id, chars)
		},
	}
}

func NewExecSessionStart(workDir string, manager *ExecSessionManager, approve ApprovalFunc) Tool {
	return NewExecSessionStartWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir), manager, approve)
}

func NewExecSessionStartWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider, manager *ExecSessionManager, approve ApprovalFunc) Tool {
	if manager == nil {
		manager = NewExecSessionManager()
	}
	return Tool{
		Name:        "exec_session_start",
		Description: "Start an interactive PTY-backed terminal session for long-running or interactive shell work.",
		Parameters: []ParameterDef{
			{Name: "command", Type: "string", Description: "command to run inside the PTY session", Required: true},
			{Name: "cols", Type: "int", Description: "terminal width in columns", Required: false},
			{Name: "rows", Type: "int", Description: "terminal height in rows", Required: false},
		},
		AutoApprove: false,
		Concurrency: ToolConcurrencySerial,
		Detached:    true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			command, _ := args["command"].(string)
			command = normalizePseudoToolCommands(command)
			approved, err := approve(Action{
				Context: ctx,
				Tool:    "exec_session_start",
				Summary: command,
				Detail:  command,
			})
			if err != nil {
				return "", err
			}
			if !approved {
				return "exec_session_start denied by user", nil
			}
			cols := intArg(args["cols"], 80)
			rows := intArg(args["rows"], 24)
			activeWorkDir := currentWorkDir(provider, fallbackWorkDir)
			if err := os.MkdirAll(activeWorkDir, 0o755); err != nil {
				return "", err
			}
			sessionID, err := manager.StartPTY(activeWorkDir, command, cols, rows)
			if err != nil {
				return "", err
			}
			payload, err := json.Marshal(execSessionStatus{
				Status:    "running",
				SessionID: sessionID,
				Command:   command,
				PTY:       true,
				Cols:      cols,
				Rows:      rows,
			})
			if err != nil {
				return "", err
			}
			return string(payload), nil
		},
	}
}

func NewExecSessionResize(manager *ExecSessionManager) Tool {
	return Tool{
		Name:        "exec_session_resize",
		Description: "Resize an interactive exec session PTY.",
		Parameters: []ParameterDef{
			{Name: "session_id", Type: "int", Description: "exec session id", Required: true},
			{Name: "cols", Type: "int", Description: "terminal width in columns", Required: true},
			{Name: "rows", Type: "int", Description: "terminal height in rows", Required: true},
		},
		AutoApprove: true,
		Concurrency: ToolConcurrencySerial,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			id := intArg(args["session_id"], 0)
			return manager.Resize(id, intArg(args["cols"], 0), intArg(args["rows"], 0))
		},
	}
}

func NewExecSessionStop(manager *ExecSessionManager) Tool {
	return Tool{
		Name:        "exec_session_stop",
		Description: "Stop an interactive exec session.",
		Parameters: []ParameterDef{
			{Name: "session_id", Type: "int", Description: "exec session id", Required: true},
		},
		AutoApprove: true,
		Concurrency: ToolConcurrencySerial,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			return manager.Stop(intArg(args["session_id"], 0))
		},
	}
}

func intArg(value any, fallback int) int {
	switch raw := value.(type) {
	case int:
		if raw != 0 {
			return raw
		}
	case float64:
		if raw != 0 {
			return int(raw)
		}
	}
	return fallback
}
