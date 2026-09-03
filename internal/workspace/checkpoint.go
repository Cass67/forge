package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	// PatchHash names the shared patch file under patches/. Empty means the
	// checkpoint predates content-addressed storage and owns <id>.patch.
	PatchHash string `json:"patch_hash,omitempty"`
}

// Retention bounds the checkpoint store. The patch is the whole working-tree
// diff against HEAD, rewritten every turn, so an unbounded store grows without
// limit — 2.8 GB across 25k checkpoints in one repo before this was added.
// Nothing outside the current session can list or restore a checkpoint anyway
// (Runner.Checkpoints reads an in-memory map), so old entries are unreachable
// rather than useful.
const (
	maxCheckpoints   = 200
	maxCheckpointAge = 7 * 24 * time.Hour
)

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
	// Patches are content-addressed: identical working trees share one file
	// instead of writing a fresh multi-megabyte copy per turn.
	sum := sha256.Sum256([]byte(patch))
	cp.PatchHash = hex.EncodeToString(sum[:])
	patchDir := filepath.Join(storeDir, "patches")
	if err := os.MkdirAll(patchDir, 0o700); err != nil {
		return Checkpoint{}, fmt.Errorf("create checkpoint: prepare patch store: %w", err)
	}
	patchPath := filepath.Join(patchDir, cp.PatchHash+".patch")
	if _, err := os.Stat(patchPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(patchPath, []byte(patch), 0o600); err != nil {
			return Checkpoint{}, fmt.Errorf("create checkpoint: write patch: %w", err)
		}
	}

	meta, err := json.Marshal(cp)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("create checkpoint: encode metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, cp.ID+".json"), meta, 0o600); err != nil {
		return Checkpoint{}, fmt.Errorf("create checkpoint: write metadata: %w", err)
	}
	// Best effort: a full store is not a reason to fail the turn.
	_ = pruneCheckpoints(storeDir)
	return cp, nil
}

// pruneCheckpoints drops checkpoints past the count or age limit, then deletes
// any patch file no longer referenced by a surviving checkpoint.
func pruneCheckpoints(storeDir string) error {
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		return err
	}
	type record struct {
		name    string
		created time.Time
	}
	var records []record
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		records = append(records, record{name: entry.Name(), created: info.ModTime()})
	}
	slices.SortFunc(records, func(a, b record) int { return b.created.Compare(a.created) })

	cutoff := time.Now().Add(-maxCheckpointAge)
	for i, rec := range records {
		if i < maxCheckpoints && rec.created.After(cutoff) {
			continue
		}
		id := strings.TrimSuffix(rec.name, ".json")
		_ = os.Remove(filepath.Join(storeDir, rec.name))
		// Pre-dedupe checkpoints owned their patch outright.
		_ = os.Remove(filepath.Join(storeDir, id+".patch"))
	}
	return collectPatches(storeDir)
}

// collectPatches removes shared patch files that no surviving checkpoint names.
func collectPatches(storeDir string) error {
	patchDir := filepath.Join(storeDir, "patches")
	patches, err := os.ReadDir(patchDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		return err
	}
	referenced := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(storeDir, entry.Name()))
		if err != nil {
			continue
		}
		var cp Checkpoint
		if err := json.Unmarshal(raw, &cp); err != nil {
			continue
		}
		if cp.PatchHash != "" {
			referenced[cp.PatchHash] = true
		}
	}
	for _, patch := range patches {
		hash := strings.TrimSuffix(patch.Name(), ".patch")
		if !referenced[hash] {
			_ = os.Remove(filepath.Join(patchDir, patch.Name()))
		}
	}
	return nil
}

// checkpointPatchPath resolves where a checkpoint's patch lives, tolerating
// stores written before patches were content-addressed.
func checkpointPatchPath(storeDir string, cp Checkpoint) string {
	if cp.PatchHash != "" {
		return filepath.Join(storeDir, "patches", cp.PatchHash+".patch")
	}
	return filepath.Join(storeDir, strings.TrimSpace(cp.ID)+".patch")
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
	patchPath := checkpointPatchPath(storeDir, cp)
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
