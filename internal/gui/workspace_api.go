package gui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/creack/pty"
)

const maxWorkspaceFileBytes = 4 << 20

type WorkspaceEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

type WorkspaceFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Version string `json:"version"`
}

type GitFileStatus struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type GitStatusResult struct {
	Repository bool            `json:"repository"`
	Branch     string          `json:"branch"`
	Files      []GitFileStatus `json:"files"`
}

type TerminalEvent struct {
	ID     string `json:"id"`
	Data   string `json:"data,omitempty"`
	Closed bool   `json:"closed,omitempty"`
}

type terminalSession struct {
	mu   sync.Mutex
	ptmx *os.File
}

func (s *Service) workspaceRoot() (string, error) {
	cfg, _, ready := s.snapshot()
	if !ready {
		return "", errNotReady
	}
	return filepath.EvalSymlinks(cfg.WorkDir)
}

func workspacePath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", errors.New("workspace path must be relative")
	}
	clean := filepath.Clean(relative)
	if clean == "." {
		return root, nil
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("workspace path escapes the workspace")
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("workspace path escapes the workspace")
	}
	return path, nil
}

func (s *Service) ListWorkspaceDir(relative string) ([]WorkspaceEntry, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return nil, err
	}
	dir, err := workspacePath(root, relative)
	if err != nil {
		return nil, err
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	result := make([]WorkspaceEntry, 0, len(items))
	for _, item := range items {
		if item.Name() == ".git" {
			continue
		}
		rel := filepath.Join(filepath.Clean(relative), item.Name())
		if filepath.Clean(relative) == "." {
			rel = item.Name()
		}
		if workspaceIgnored(root, rel) {
			continue
		}
		info, infoErr := item.Info()
		if infoErr != nil {
			continue
		}
		result = append(result, WorkspaceEntry{Name: item.Name(), Path: filepath.ToSlash(rel), IsDir: item.IsDir(), Size: info.Size()})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

func workspaceIgnored(root, relative string) bool {
	cmd := exec.Command("git", "check-ignore", "--quiet", "--", relative)
	cmd.Dir = root
	return cmd.Run() == nil
}

func (s *Service) ReadWorkspaceFile(relative string) (WorkspaceFile, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return WorkspaceFile{}, err
	}
	path, err := workspacePath(root, relative)
	if err != nil {
		return WorkspaceFile{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if !info.Mode().IsRegular() {
		return WorkspaceFile{}, errors.New("workspace path is not a regular file")
	}
	if info.Size() > maxWorkspaceFileBytes {
		return WorkspaceFile{}, fmt.Errorf("file exceeds %d MiB editor limit", maxWorkspaceFileBytes>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return WorkspaceFile{}, errors.New("binary files cannot be edited")
	}
	return WorkspaceFile{Path: filepath.ToSlash(filepath.Clean(relative)), Content: string(data), Version: fileVersion(data)}, nil
}

func (s *Service) WriteWorkspaceFile(relative, content, expectedVersion string) (WorkspaceFile, error) {
	if len(content) > maxWorkspaceFileBytes {
		return WorkspaceFile{}, fmt.Errorf("file exceeds %d MiB editor limit", maxWorkspaceFileBytes>>20)
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return WorkspaceFile{}, err
	}
	path, err := workspacePath(root, relative)
	if err != nil {
		return WorkspaceFile{}, err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if fileVersion(current) != expectedVersion {
		return WorkspaceFile{}, errors.New("file changed on disk; reload before saving")
	}
	info, err := os.Stat(path)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if err := replaceWorkspaceFile(path, []byte(content), info.Mode().Perm()); err != nil {
		return WorkspaceFile{}, err
	}
	return WorkspaceFile{Path: filepath.ToSlash(filepath.Clean(relative)), Content: content, Version: fileVersion([]byte(content))}, nil
}

func replaceWorkspaceFile(path string, data []byte, mode os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".forge-save-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if err = tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func fileVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Service) GitStatus() (GitStatusResult, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return GitStatusResult{}, err
	}
	cmd := exec.Command("git", "status", "--porcelain=v1", "-z", "--branch", "--untracked-files=all")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(out)), "not a git repository") {
			return GitStatusResult{Files: []GitFileStatus{}}, nil
		}
		return GitStatusResult{}, fmt.Errorf("git status: %s", strings.TrimSpace(string(out)))
	}
	parts := bytes.Split(out, []byte{0})
	result := GitStatusResult{Repository: true, Files: []GitFileStatus{}}
	for i := 0; i < len(parts); i++ {
		line := string(parts[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			result.Branch = strings.TrimPrefix(line, "## ")
			continue
		}
		if len(line) < 4 {
			continue
		}
		status, path := line[:2], line[3:]
		if status[0] == 'R' || status[0] == 'C' {
			i++
		}
		result.Files = append(result.Files, GitFileStatus{Path: path, Status: status})
	}
	return result, nil
}

func (s *Service) StartTerminal(id string, rows, cols int) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("terminal id is required")
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return err
	}
	if rows < 1 {
		rows = 24
	}
	if cols < 1 {
		cols = 80
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir = root
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return err
	}
	s.mu.Lock()
	if _, exists := s.terminals[id]; exists {
		s.mu.Unlock()
		_ = ptmx.Close()
		return errors.New("terminal already exists")
	}
	session := &terminalSession{ptmx: ptmx}
	s.terminals[id] = session
	s.mu.Unlock()
	go func() {
		buf := make([]byte, 8192)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				s.emit(EventTerminal, TerminalEvent{ID: id, Data: string(buf[:n])})
			}
			if readErr != nil {
				break
			}
		}
		s.mu.Lock()
		if s.terminals[id] == session {
			delete(s.terminals, id)
		}
		s.mu.Unlock()
		_ = ptmx.Close()
		s.emit(EventTerminal, TerminalEvent{ID: id, Closed: true})
	}()
	return nil
}

func (s *Service) WriteTerminal(id, data string) error {
	s.mu.RLock()
	session := s.terminals[id]
	s.mu.RUnlock()
	if session == nil {
		return errors.New("terminal not found")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	_, err := io.WriteString(session.ptmx, data)
	return err
}

func (s *Service) ResizeTerminal(id string, rows, cols int) error {
	if rows < 1 || cols < 1 {
		return errors.New("terminal size must be positive")
	}
	s.mu.RLock()
	session := s.terminals[id]
	s.mu.RUnlock()
	if session == nil {
		return errors.New("terminal not found")
	}
	return pty.Setsize(session.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

func (s *Service) CloseTerminal(id string) error {
	s.mu.Lock()
	session := s.terminals[id]
	delete(s.terminals, id)
	s.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.ptmx.Close()
}

func (s *Service) closeTerminals() {
	s.mu.Lock()
	sessions := make([]*terminalSession, 0, len(s.terminals))
	for id, session := range s.terminals {
		sessions = append(sessions, session)
		delete(s.terminals, id)
	}
	s.mu.Unlock()
	for _, session := range sessions {
		_ = session.ptmx.Close()
	}
}
