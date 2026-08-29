package sessionstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type OutputHandle struct {
	ID     string
	Bytes  int
	SHA256 string
}

type OutputStore interface {
	Put(ctx context.Context, threadID string, data []byte) (OutputHandle, error)
	Handle(ctx context.Context, id string) (OutputHandle, error)
	Read(ctx context.Context, handle OutputHandle, offset, limit int64) ([]byte, error)
}

type FileOutputStore struct {
	root string
}

func NewFileOutputStore(root string) *FileOutputStore {
	return &FileOutputStore{root: root}
}

func (s *FileOutputStore) Put(ctx context.Context, threadID string, data []byte) (OutputHandle, error) {
	if err := ctxErr(ctx); err != nil {
		return OutputHandle{}, err
	}
	if s == nil || strings.TrimSpace(s.root) == "" {
		return OutputHandle{}, fmt.Errorf("output store root is empty")
	}
	thread, ok := validHandlePart(threadID)
	if !ok {
		return OutputHandle{}, fmt.Errorf("invalid output thread ID")
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	outputRoot := s.outputRoot()
	if err := ensureOutputRoot(outputRoot); err != nil {
		return OutputHandle{}, err
	}
	dir := filepath.Join(s.outputRoot(), thread)
	if err := ensureInside(outputRoot, dir); err != nil {
		return OutputHandle{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return OutputHandle{}, err
	}
	if err := rejectSymlink(dir); err != nil {
		return OutputHandle{}, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return OutputHandle{}, err
	}
	path := filepath.Join(dir, hash+".out")
	if err := ensureInside(outputRoot, path); err != nil {
		return OutputHandle{}, err
	}
	if ok, err := existingOutputMatches(path, hash, len(data)); err != nil || ok {
		if ok {
			err = os.Chmod(path, 0o600)
		}
		return OutputHandle{ID: thread + "/" + hash, Bytes: len(data), SHA256: hash}, err
	}
	if err := writeFileAtomic(dir, path, data); err != nil {
		return OutputHandle{}, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return OutputHandle{}, err
	}
	return OutputHandle{ID: thread + "/" + hash, Bytes: len(data), SHA256: hash}, nil
}

func (s *FileOutputStore) Read(ctx context.Context, handle OutputHandle, offset, limit int64) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []byte{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	path, err := s.pathForHandle(handle)
	if err != nil {
		return nil, err
	}
	if ok, err := existingOutputMatches(path, handle.SHA256, handle.Bytes); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("output file does not match handle metadata")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if offset >= int64(len(data)) {
		return []byte{}, nil
	}
	end := offset + limit
	if end < offset || end > int64(len(data)) {
		end = int64(len(data))
	}
	return data[offset:end], nil
}

func (s *FileOutputStore) Handle(ctx context.Context, id string) (OutputHandle, error) {
	if err := ctxErr(ctx); err != nil {
		return OutputHandle{}, err
	}
	_, hash, ok := parseHandleID(id)
	if !ok {
		return OutputHandle{}, errInvalidHandle(id)
	}
	handle := OutputHandle{ID: strings.TrimSpace(id), SHA256: hash}
	path, err := s.pathForHandle(handle)
	if err != nil {
		return OutputHandle{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return OutputHandle{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return OutputHandle{}, fmt.Errorf("output file is symlink")
	}
	if !info.Mode().IsRegular() {
		return OutputHandle{}, fmt.Errorf("output file is not regular")
	}
	handle.Bytes = int(info.Size())
	return handle, nil
}

func (s *FileOutputStore) pathForHandle(handle OutputHandle) (string, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return "", fmt.Errorf("output store root is empty")
	}
	thread, hash, ok := parseHandleID(handle.ID)
	if !ok {
		return "", errInvalidHandle(handle.ID)
	}
	outputRoot := s.outputRoot()
	if err := rejectSymlink(outputRoot); err != nil {
		return "", err
	}
	threadDir := filepath.Join(outputRoot, thread)
	if err := rejectSymlink(threadDir); err != nil {
		return "", err
	}
	path := filepath.Join(threadDir, hash+".out")
	if err := ensureInside(outputRoot, path); err != nil {
		return "", err
	}
	return path, nil
}

func (s *FileOutputStore) outputRoot() string {
	return filepath.Join(s.root, "outputs")
}

func errInvalidHandle(id string) error {
	// A bare integer is a background command session, not an output handle.
	// Saying so routes the caller instead of leaving it to guess again: observed
	// a model try wait_agent, then read_output, then fall back to sleeping.
	if trimmed := strings.TrimSpace(id); trimmed != "" && isAllDigits(trimmed) {
		return fmt.Errorf("%q is a background command session id, not an output handle: check it with command_status {\"session_id\": %s}. Output handles look like \"<thread>/<sha256-hex>\" and are only issued in a stored-output message", trimmed, trimmed)
	}
	return fmt.Errorf("invalid output handle %q: expected \"<thread>/<sha256-hex>\" exactly as shown after \"Handle:\" in the stored-output message; tool call IDs are not output handles", id)
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseHandleID(id string) (string, string, bool) {
	parts := strings.Split(strings.TrimSpace(id), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	if _, ok := validHandlePart(parts[0]); !ok || !isHexSHA256(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func validHandlePart(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." {
		return "", false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return "", false
		}
	}
	return value, true
}

func isHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'f') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func ensureInside(root, path string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return fmt.Errorf("output path escapes output root")
	}
	return nil
}

func ensureOutputRoot(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := rejectSymlink(path); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("output path contains symlink")
	}
	return nil
}

func existingOutputMatches(path, hash string, size int) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("output file is symlink")
	}
	if !info.Mode().IsRegular() || info.Size() != int64(size) {
		return false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return false, err
	}
	return hex.EncodeToString(sum.Sum(nil)) == hash, nil
}

func writeFileAtomic(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*.out")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
