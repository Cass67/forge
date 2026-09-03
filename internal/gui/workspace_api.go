package gui

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

type TerminalEvent struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace,omitempty"`
	Data      string `json:"data,omitempty"`
	Closed    bool   `json:"closed,omitempty"`
}

type terminalSession struct {
	mu     sync.Mutex
	ptmx   *os.File
	buffer []byte
}

const terminalBufferLimit = 4 << 20

func terminalEnvironment(env []string) []string {
	result := make([]string, 0, len(env)+2)
	for _, entry := range env {
		if strings.HasPrefix(entry, "TERM=") || strings.HasPrefix(entry, "COLORTERM=") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "TERM=xterm-256color", "COLORTERM=truecolor")
}

// workspaceRoot is the directory the panels are looking at: the file tree, the
// editor, source control and worktrees. Normally that is the workspace the
// chat is in, but a workspace can be browsed without starting a chat in it, in
// which case the panels follow the browse and the chat stays where it is.
func (s *Service) workspaceRoot() (string, error) {
	s.mu.RLock()
	browsing := s.browseDir
	s.mu.RUnlock()
	if browsing != "" {
		return filepath.EvalSymlinks(browsing)
	}
	return s.chatRoot()
}

// chatRoot is the workspace the conversation on screen is running in,
// whatever the panels are pointed at. Terminals use it: a shell is keyed to
// the workspace that owns it, not to whatever is being browsed.
func (s *Service) chatRoot() (string, error) {
	cfg, _, ready := s.snapshot()
	if !ready {
		return "", errNotReady
	}
	return filepath.EvalSymlinks(cfg.WorkDir)
}

// mutableRoot is workspaceRoot for anything that changes a repository or a
// file. Browsing points the panels at a workspace with no chat running in it,
// and changing files there is how you end up committing to the wrong
// repository: the answer is to open it, which moves the chat with you.
// Every mutating bound method starts here.
func (s *Service) mutableRoot() (string, error) {
	s.mu.RLock()
	browsing := s.browseDir
	s.mu.RUnlock()
	if browsing != "" {
		return "", fmt.Errorf("%w: %s", errBrowsing, filepath.Base(browsing))
	}
	return s.chatRoot()
}

// SetExplorerRoot points the panels at a directory without starting anything
// there. An empty dir hands them back to the workspace the chat is in.
func (s *Service) SetExplorerRoot(dir string) error {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		s.mu.Lock()
		s.browseDir = ""
		s.mu.Unlock()
		return nil
	}
	clean, err := workspaceDir(trimmed)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.browseDir = clean
	s.mu.Unlock()
	return nil
}

// SetScratchDir persists and immediately activates the Explorer scratch root.
func (s *Service) SetScratchDir(dir string) (string, error) {
	trimmed := strings.TrimSpace(dir)
	if s.SaveScratchDir == nil {
		return "", errNotReady
	}
	resolved, err := s.SaveScratchDir(trimmed)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.ScratchDir = resolved
	s.mu.Unlock()
	return resolved, nil
}

// ExplorerRoot reports the directory the panels are on.
func (s *Service) ExplorerRoot() string {
	root, err := s.workspaceRoot()
	if err != nil {
		return ""
	}
	return root
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

// workspaceNewPath resolves a relative path that does not exist yet (used by
// create and copy). The final component is not required to exist, but its
// parent directory is resolved through symlinks so a create can never write
// outside the workspace through a link.
func workspaceNewPath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", errors.New("workspace path must be relative")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("workspace path escapes the workspace")
	}
	parentReal, err := filepath.EvalSymlinks(filepath.Dir(filepath.Join(root, clean)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, parentReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("workspace path escapes the workspace")
	}
	return filepath.Join(parentReal, filepath.Base(clean)), nil
}

func (s *Service) ListWorkspaceDir(relative string) ([]WorkspaceEntry, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return nil, err
	}
	dir, err := workspacePath(root, relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
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
	slices.SortFunc(result, func(a, b WorkspaceEntry) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
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
	if _, err := s.mutableRoot(); err != nil {
		return WorkspaceFile{}, err
	}
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

// scratchRoot resolves the directory the throwaway scratch files live in and
// makes sure it exists. Unlike the workspace, scratch is not bound to a chat
// workspace: it is junk, so nothing is gated on being able to write to a repo.
func (s *Service) scratchRoot() (string, error) {
	s.mu.RLock()
	dir := strings.TrimSpace(s.ScratchDir)
	s.mu.RUnlock()
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// scratchName rejects traversal: a scratch file is a single name inside the
// scratch root, never a path.
func scratchName(name string) (string, error) {
	name = strings.TrimSpace(name)
	base := filepath.Base(name)
	if name == "" || name == "." || name == ".." || base != name {
		return "", errors.New("scratch name must be a plain file name")
	}
	return name, nil
}

func (s *Service) CreateWorkspaceFile(relative, content string) (WorkspaceFile, error) {
	if _, err := s.mutableRoot(); err != nil {
		return WorkspaceFile{}, err
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return WorkspaceFile{}, err
	}
	path, err := workspaceNewPath(root, relative)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return WorkspaceFile{}, err
	}
	if _, err := os.Stat(path); err == nil {
		return WorkspaceFile{}, errors.New("workspace file already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return WorkspaceFile{}, err
	}
	if len(content) > maxWorkspaceFileBytes {
		return WorkspaceFile{}, fmt.Errorf("file exceeds %d MiB editor limit", maxWorkspaceFileBytes>>20)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return WorkspaceFile{}, err
	}
	return WorkspaceFile{
		Path:    filepath.ToSlash(filepath.Clean(relative)),
		Content: content,
		Version: fileVersion([]byte(content)),
	}, nil
}

func (s *Service) CreateWorkspaceDir(relative string) error {
	if _, err := s.mutableRoot(); err != nil {
		return err
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return err
	}
	path, err := workspaceNewPath(root, relative)
	if err != nil {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

func (s *Service) DeleteWorkspacePath(relative string) error {
	if _, err := s.mutableRoot(); err != nil {
		return err
	}
	if strings.TrimSpace(relative) == "." {
		return errors.New("cannot delete the workspace root")
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return err
	}
	path, err := workspacePath(root, relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.RemoveAll(path)
}

func (s *Service) RenameWorkspacePath(from, to string) error {
	if _, err := s.mutableRoot(); err != nil {
		return err
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return err
	}
	src, err := workspacePath(root, from)
	if err != nil {
		return err
	}
	dst, err := workspaceNewPath(root, to)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

func (s *Service) CopyWorkspacePath(from, to string) error {
	if _, err := s.mutableRoot(); err != nil {
		return err
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return err
	}
	src, err := workspacePath(root, from)
	if err != nil {
		return err
	}
	dst, err := workspaceNewPath(root, to)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		return filepath.WalkDir(src, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(src, p)
			if err != nil {
				return err
			}
			target := filepath.Join(dst, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			mode := os.FileMode(0o600)
			if fi, err := d.Info(); err == nil {
				mode = fi.Mode().Perm()
			}
			return copyFile(p, target, mode)
		})
	}
	mode := info.Mode().Perm()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyFile(src, dst, mode)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// — Scratch files —

func (s *Service) ScratchRoot() string {
	dir, err := s.scratchRoot()
	if err != nil {
		return ""
	}
	return dir
}

func (s *Service) ListScratch() ([]WorkspaceEntry, error) {
	root, err := s.scratchRoot()
	if err != nil {
		return nil, err
	}
	items, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	result := make([]WorkspaceEntry, 0, len(items))
	for _, item := range items {
		info, infoErr := item.Info()
		if infoErr != nil {
			continue
		}
		result = append(result, WorkspaceEntry{
			Name:  item.Name(),
			Path:  item.Name(),
			IsDir: item.IsDir(),
			Size:  info.Size(),
		})
	}
	slices.SortFunc(result, func(a, b WorkspaceEntry) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return result, nil
}

func (s *Service) CreateScratchFile(name, content string) (WorkspaceFile, error) {
	name, err := scratchName(name)
	if err != nil {
		return WorkspaceFile{}, err
	}
	root, err := s.scratchRoot()
	if err != nil {
		return WorkspaceFile{}, err
	}
	path := filepath.Join(root, name)
	if _, err := os.Stat(path); err == nil {
		return WorkspaceFile{}, errors.New("scratch file already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return WorkspaceFile{}, err
	}
	if len(content) > maxWorkspaceFileBytes {
		return WorkspaceFile{}, fmt.Errorf("file exceeds %d MiB editor limit", maxWorkspaceFileBytes>>20)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return WorkspaceFile{}, err
	}
	return WorkspaceFile{Path: name, Content: content, Version: fileVersion([]byte(content))}, nil
}

func (s *Service) ReadScratchFile(name string) (WorkspaceFile, error) {
	name, err := scratchName(name)
	if err != nil {
		return WorkspaceFile{}, err
	}
	root, err := s.scratchRoot()
	if err != nil {
		return WorkspaceFile{}, err
	}
	path := filepath.Join(root, name)
	info, err := os.Stat(path)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if !info.Mode().IsRegular() {
		return WorkspaceFile{}, errors.New("scratch path is not a regular file")
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
	return WorkspaceFile{Path: name, Content: string(data), Version: fileVersion(data)}, nil
}

func (s *Service) WriteScratchFile(name, content, expectedVersion string) (WorkspaceFile, error) {
	name, err := scratchName(name)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if len(content) > maxWorkspaceFileBytes {
		return WorkspaceFile{}, fmt.Errorf("file exceeds %d MiB editor limit", maxWorkspaceFileBytes>>20)
	}
	root, err := s.scratchRoot()
	if err != nil {
		return WorkspaceFile{}, err
	}
	path := filepath.Join(root, name)
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
	return WorkspaceFile{Path: name, Content: content, Version: fileVersion([]byte(content))}, nil
}

func (s *Service) DeleteScratchFile(name string) error {
	name, err := scratchName(name)
	if err != nil {
		return err
	}
	root, err := s.scratchRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(root, name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return os.RemoveAll(path)
}

func (s *Service) RenameScratchFile(from, to string) error {
	from, err := scratchName(from)
	if err != nil {
		return err
	}
	to, err = scratchName(to)
	if err != nil {
		return err
	}
	root, err := s.scratchRoot()
	if err != nil {
		return err
	}
	return os.Rename(filepath.Join(root, from), filepath.Join(root, to))
}

func (s *Service) CopyScratchFile(from, to string) error {
	from, err := scratchName(from)
	if err != nil {
		return err
	}
	to, err = scratchName(to)
	if err != nil {
		return err
	}
	root, err := s.scratchRoot()
	if err != nil {
		return err
	}
	src := filepath.Join(root, from)
	if _, err := os.Stat(src); err != nil {
		return err
	}
	return copyFile(src, filepath.Join(root, to), 0o600)
}

func (s *Service) StartTerminal(id string, rows, cols int) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("terminal id is required")
	}
	// Shells belong to the workspace the chat is in, not to whatever is being
	// browsed: they are keyed by it, and they outlive a look at another repo.
	root, err := s.chatRoot()
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	key := s.terminalKey(dir, id)
	s.mu.RLock()
	existing := s.terminals[key]
	s.mu.RUnlock()
	if existing != nil {
		existing.mu.Lock()
		output := string(existing.buffer)
		existing.mu.Unlock()
		return output, nil
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
	cmd.Env = terminalEnvironment(os.Environ())
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	if _, exists := s.terminals[key]; exists {
		s.mu.Unlock()
		_ = ptmx.Close()
		return s.StartTerminal(id, rows, cols)
	}
	session := &terminalSession{ptmx: ptmx}
	s.terminals[key] = session
	s.mu.Unlock()
	go func() {
		buf := make([]byte, 8192)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				session.mu.Lock()
				session.buffer = append(session.buffer, buf[:n]...)
				if extra := len(session.buffer) - terminalBufferLimit; extra > 0 {
					session.buffer = append(session.buffer[:0], session.buffer[extra:]...)
				}
				session.mu.Unlock()
				s.emit(EventTerminal, TerminalEvent{ID: id, Workspace: dir, Data: string(buf[:n])})
			}
			if readErr != nil {
				break
			}
		}
		s.mu.Lock()
		if s.terminals[key] == session {
			delete(s.terminals, key)
		}
		s.mu.Unlock()
		_ = ptmx.Close()
		s.emit(EventTerminal, TerminalEvent{ID: id, Workspace: dir, Closed: true})
	}()
	return "", nil
}

// terminalKey scopes a terminal to its workspace, so switching workspaces
// never collides with or closes another directory's terminals.
func (s *Service) terminalKey(dir, id string) string {
	return cleanDir(dir) + "/" + id
}

// activeTerminalKey resolves a frontend terminal id against the active
// workspace. Callers hold s.mu: re-entering it here would deadlock, because a
// blocked writer parks between the outer read lock and this one.
func (s *Service) activeTerminalKey(id string) string {
	if s.activeDir == "" {
		return ""
	}
	root, err := filepath.EvalSymlinks(s.activeDir)
	if err != nil {
		return s.activeDir + "/" + id
	}
	return root + "/" + id
}

func (s *Service) WriteTerminal(id, data string) error {
	s.mu.RLock()
	session := s.terminals[s.activeTerminalKey(id)]
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
	session := s.terminals[s.activeTerminalKey(id)]
	s.mu.RUnlock()
	if session == nil {
		return errors.New("terminal not found")
	}
	return pty.Setsize(session.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

func (s *Service) CloseTerminal(id string) error {
	s.mu.Lock()
	key := s.activeTerminalKey(id)
	session := s.terminals[key]
	delete(s.terminals, key)
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
