package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var ErrUnsupportedCheckpoint = errors.New("workspace checkpoints unsupported")
var ErrCheckpointScopeRequired = errors.New("workspace checkpoint restore scope required")

type Checkpoint struct {
	ID           string    `json:"id"`
	TurnID       string    `json:"turn_id"`
	Root         string    `json:"root"`
	CreatedAt    time.Time `json:"created_at"`
	ChangedFiles []string  `json:"changed_files,omitempty"`
	RestoreFiles []string  `json:"restore_files,omitempty"`
	BaseCommit   string    `json:"base_commit,omitempty"`
}

type CheckpointManager struct {
	root string
}

func NewCheckpointManager(root string) *CheckpointManager {
	return &CheckpointManager{root: root}
}

func (m *CheckpointManager) Create(ctx context.Context, turnID string) (Checkpoint, error) {
	root, err := m.gitRoot(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	baseCommit, err := runGitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return Checkpoint{}, fmt.Errorf("create checkpoint: git repository has no HEAD: %w", err)
	}
	changedOutput, err := runGitOutput(ctx, root, "diff", "--name-only", "-z", "HEAD", "--")
	if err != nil {
		return Checkpoint{}, fmt.Errorf("create checkpoint: list tracked changes: %w", err)
	}
	patch, err := runGitOutput(ctx, root, "diff", "--binary", "HEAD", "--")
	if err != nil {
		return Checkpoint{}, fmt.Errorf("create checkpoint: capture tracked changes: %w", err)
	}

	cp := Checkpoint{
		ID:           checkpointID(turnID),
		TurnID:       turnID,
		Root:         root,
		CreatedAt:    time.Now().UTC(),
		ChangedFiles: changedFiles(changedOutput),
		BaseCommit:   strings.TrimSpace(baseCommit),
	}
	storeDir, err := checkpointStoreDir(ctx, root)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("create checkpoint: resolve store: %w", err)
	}
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		return Checkpoint{}, fmt.Errorf("create checkpoint: prepare store: %w", err)
	}
	meta, err := json.Marshal(cp)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("create checkpoint: encode metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, cp.ID+".json"), meta, 0o600); err != nil {
		return Checkpoint{}, fmt.Errorf("create checkpoint: write metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, cp.ID+".patch"), []byte(patch), 0o600); err != nil {
		return Checkpoint{}, fmt.Errorf("create checkpoint: write patch: %w", err)
	}
	return cp, nil
}

func (m *CheckpointManager) RecordChangedFiles(ctx context.Context, checkpointID string, files []string) error {
	root, err := m.gitRoot(ctx)
	if err != nil {
		return err
	}
	cp, metaPath, err := m.loadCheckpoint(ctx, root, checkpointID)
	if err != nil {
		return err
	}
	cp.RestoreFiles = appendUniquePaths(cp.RestoreFiles, files...)
	meta, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("record checkpoint files: encode metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, meta, 0o600); err != nil {
		return fmt.Errorf("record checkpoint files: write metadata: %w", err)
	}
	return nil
}

func (m *CheckpointManager) Restore(ctx context.Context, checkpointID string) error {
	root, err := m.gitRoot(ctx)
	if err != nil {
		return err
	}
	cp, _, err := m.loadCheckpoint(ctx, root, checkpointID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cp.BaseCommit) == "" {
		return fmt.Errorf("restore checkpoint: missing base commit")
	}
	restoreFiles := cp.RestoreFiles
	if len(restoreFiles) == 0 {
		// Without explicit tool scope, do not destructively restore post-checkpoint
		// edits. A clean checkpoint cannot distinguish tool changes from unrelated
		// user edits made later; callers should RecordChangedFiles when they know scope.
		currentChanged, err := runGitOutput(ctx, root, "diff", "--name-only", "-z", cp.BaseCommit, "--")
		if err != nil {
			return fmt.Errorf("restore checkpoint: list current changes: %w", err)
		}
		if len(excludePaths(changedFiles(currentChanged), cp.ChangedFiles)) > 0 {
			return fmt.Errorf("%w: changed files without explicit scope", ErrCheckpointScopeRequired)
		}
	}
	if len(restoreFiles) == 0 {
		return nil
	}
	restoreArgs := append([]string{"restore", "--source", cp.BaseCommit, "--worktree", "--staged", "--"}, restoreFiles...)
	if _, err := runGitOutput(ctx, root, restoreArgs...); err != nil {
		return fmt.Errorf("restore checkpoint: reset tracked files: %w", err)
	}
	storeDir, err := checkpointStoreDir(ctx, root)
	if err != nil {
		return fmt.Errorf("restore checkpoint: resolve store: %w", err)
	}
	patchPath := filepath.Join(storeDir, strings.TrimSpace(checkpointID)+".patch")
	patch, err := os.ReadFile(patchPath)
	if err != nil {
		return fmt.Errorf("restore checkpoint: read patch: %w", err)
	}
	if len(strings.TrimSpace(string(patch))) == 0 {
		return nil
	}
	applyArgs := []string{"apply", "--binary", "--whitespace=nowarn"}
	for _, file := range restoreFiles {
		applyArgs = append(applyArgs, "--include", file)
	}
	applyArgs = append(applyArgs, patchPath)
	if _, err := runGitOutput(ctx, root, applyArgs...); err != nil {
		return fmt.Errorf("restore checkpoint: apply checkpoint patch: %w", err)
	}
	return nil
}

func (m *CheckpointManager) loadCheckpoint(ctx context.Context, root, checkpointID string) (Checkpoint, string, error) {
	storeDir, err := checkpointStoreDir(ctx, root)
	if err != nil {
		return Checkpoint{}, "", fmt.Errorf("checkpoint: resolve store: %w", err)
	}
	checkpointID = strings.TrimSpace(checkpointID)
	if err := validateCheckpointID(checkpointID); err != nil {
		return Checkpoint{}, "", err
	}
	metaPath := filepath.Join(storeDir, checkpointID+".json")
	meta, err := os.ReadFile(metaPath)
	if err != nil {
		return Checkpoint{}, "", fmt.Errorf("checkpoint: read metadata: %w", err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(meta, &cp); err != nil {
		return Checkpoint{}, "", fmt.Errorf("checkpoint: decode metadata: %w", err)
	}
	return cp, metaPath, nil
}

func validateCheckpointID(id string) error {
	if id == "" {
		return fmt.Errorf("invalid checkpoint ID: empty")
	}
	if id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid checkpoint ID: %q", id)
	}
	return nil
}

func (m *CheckpointManager) gitRoot(ctx context.Context) (string, error) {
	root := strings.TrimSpace(m.root)
	if root == "" {
		root = "."
	}
	out, err := runGitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%w: git repository required", ErrUnsupportedCheckpoint)
	}
	return strings.TrimSpace(out), nil
}

func checkpointStoreDir(ctx context.Context, root string) (string, error) {
	out, err := runGitOutput(ctx, root, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return "", err
	}
	return filepath.Join(strings.TrimSpace(out), "forge-checkpoints"), nil
}

func checkpointID(turnID string) string {
	turnID = strings.Trim(strings.ToLower(turnID), "-_")
	var b strings.Builder
	for _, r := range turnID {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		b.WriteString("turn")
	}
	return fmt.Sprintf("%s-%d", b.String(), time.Now().UTC().UnixNano())
}

func changedFiles(output string) []string {
	if strings.Contains(output, "\x00") {
		fields := strings.Split(output, "\x00")
		files := make([]string, 0, len(fields))
		for _, field := range fields {
			field = strings.TrimSpace(field)
			if field != "" {
				files = append(files, field)
			}
		}
		return files
	}
	lines := strings.Split(output, "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func appendUniquePaths(paths []string, more ...string) []string {
	seen := make(map[string]bool, len(paths)+len(more))
	result := make([]string, 0, len(paths)+len(more))
	for _, path := range append(paths, more...) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	return result
}

func excludePaths(paths, excluded []string) []string {
	exclude := make(map[string]bool, len(excluded))
	for _, path := range excluded {
		path = strings.TrimSpace(path)
		if path != "" {
			exclude[path] = true
		}
	}
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path != "" && !exclude[path] {
			result = append(result, path)
		}
	}
	return result
}

func runGitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
