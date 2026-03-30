package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
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
	stdin    io.WriteCloser
	output   bytes.Buffer
	done     bool
	exitCode int
	onEvent  func(ExecSessionStatus)
}

type execSessionStatus struct {
	Status    string `json:"status"`
	SessionID int    `json:"session_id"`
	Command   string `json:"command"`
	Output    string `json:"output,omitempty"`
	ExitCode  int    `json:"exit_code,omitempty"`
}

type ExecSessionStatus = execSessionStatus

func NewExecSessionManager() *ExecSessionManager {
	return &ExecSessionManager{
		nextID:   1,
		sessions: make(map[int]*execSession),
	}
}

func (m *ExecSessionManager) Start(workDir, command string) (int, error) {
	if m == nil {
		return 0, fmt.Errorf("exec session manager is nil")
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return 0, fmt.Errorf("command is required")
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = workDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
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
		stdin:     stdin,
		onEvent:   m.onEvent,
	}
	m.sessions[id] = sess
	m.mu.Unlock()

	go sess.capture(stdout)
	go sess.capture(stderr)
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
			s.mu.Unlock()
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

func (s *execSession) write(chars string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return "", fmt.Errorf("session %d has already exited", s.id)
	}
	if s.stdin == nil {
		return "", fmt.Errorf("session %d does not accept input", s.id)
	}
	if _, err := io.WriteString(s.stdin, chars); err != nil {
		return "", err
	}
	payload, err := json.Marshal(execSessionStatus{
		Status:    "running",
		SessionID: s.id,
		Command:   s.command,
	})
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
	}
}

func NewCommandStatus(manager *ExecSessionManager) Tool {
	return Tool{
		Name:        "command_status",
		Description: "Check the status of a background command session.",
		Parameters: []ParameterDef{
			{Name: "session_id", Type: "int", Description: "background command session id", Required: true},
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
	return Tool{
		Name:        "command_write_stdin",
		Description: "Write input to a background command session.",
		Parameters: []ParameterDef{
			{Name: "session_id", Type: "int", Description: "background command session id", Required: true},
			{Name: "chars", Type: "string", Description: "text to write to the session stdin", Required: true},
		},
		AutoApprove: true,
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
