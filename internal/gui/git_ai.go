package gui

// Model-assisted git features: commit messages and the changes walkthrough.
// Both run off-transcript through cfg.Complete so the conversation the user is
// having is untouched.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// A diff much larger than this stops being useful to a model and starts
// costing real money, so it is truncated with a marker the prompt explains.
const maxPromptDiffBytes = 180_000

const commitSystemPrompt = `You write git commit messages.
Reply with the commit message only: no preamble, no code fences, no explanation.
Use the Conventional Commits form "type(scope): subject" when the change fits one.
Keep the subject under 72 characters, in the imperative mood, with no trailing period.
Add a body only when the change needs justification a reader could not infer from the diff; separate it with a blank line and wrap at 72 columns.`

const walkthroughSystemPrompt = `You turn a git diff into an ordered walkthrough for a reviewer.

A diff is sorted by file path, which is almost never the order in which the change makes sense. Regroup it into "stops": each stop is a coherent piece of the change, and the stops build on each other so a reader who follows them in order understands the change by the end.

Rules:
- Order stops so that each one only depends on what earlier stops established.
- Group edits that only make sense together into one stop, across files.
- Tag a stop "key" when it drives the change or carries real risk; tag it "context" when it is supporting detail a reader needs but should not dwell on; otherwise leave the tag empty.
- Explain what the code now DOES differently. Do not narrate the diff line by line.
- Reference every changed file in at least one stop.
- Keep identifiers, paths and symbol names exactly as they appear in the code.

Reply with JSON only, no code fences, matching:
{"summary":"one or two sentences on the change as a whole",
 "stops":[{"title":"short label","tag":"key|context|","files":["path/one"],"explanation":"what this part does and why it matters"}]}`

type WalkStop struct {
	Title       string   `json:"title"`
	Tag         string   `json:"tag,omitempty"`
	Files       []string `json:"files"`
	Explanation string   `json:"explanation"`
}

// Walkthrough is a generated review tour. Fingerprint is a hash of the diff it
// was built from: the frontend passes it back to WalkthroughStale to find out
// whether the code moved underneath the tour.
type Walkthrough struct {
	Scope       string     `json:"scope"`
	Base        string     `json:"base,omitempty"`
	Summary     string     `json:"summary"`
	Stops       []WalkStop `json:"stops"`
	Uncovered   []string   `json:"uncovered"`
	Fingerprint string     `json:"fingerprint"`
	Truncated   bool       `json:"truncated"`
	Model       string     `json:"model,omitempty"`
	GeneratedAt string     `json:"generated_at"`
}

// complete runs a side model call. An empty model falls through to the model
// configured for role in [models], and then to whatever Complete defaults to.
func (s *Service) complete(ctx context.Context, role, model, system, user string) (string, error) {
	cfg, _, ready := s.snapshot()
	if !ready {
		return "", errNotReady
	}
	if cfg.Complete == nil {
		return "", errors.New("this session cannot make side model calls")
	}
	if strings.TrimSpace(model) == "" && cfg.RoleModel != nil {
		model = cfg.RoleModel(role)
	}
	return cfg.Complete(ctx, model, system, user)
}

// GenerateCommitMessage drafts a message for what is currently staged, falling
// back to the whole working tree when the index is empty — which is what a
// user who has not staged anything yet almost always means.
func (s *Service) GenerateCommitMessage(model string) (string, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return "", err
	}
	diff, err := runGit(root, "diff", "--no-color", "--no-ext-diff", "--cached")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(diff) == "" {
		if diff, err = runGit(root, "diff", "--no-color", "--no-ext-diff"); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(diff) == "" {
		return "", errors.New("nothing to describe: stage some changes first")
	}
	body, truncated := truncateDiff(diff)
	prompt := "Write a commit message for this diff.\n\n"
	if truncated {
		prompt = "Write a commit message for this diff. It was truncated, so describe the change as a whole rather than claiming to cover every file.\n\n"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := s.complete(ctx, "commit", model, commitSystemPrompt, prompt+body)
	if err != nil {
		return "", err
	}
	return cleanCommitMessage(out), nil
}

// cleanCommitMessage strips the wrappers models add despite being told not to.
func cleanCommitMessage(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

func truncateDiff(diff string) (string, bool) {
	if len(diff) <= maxPromptDiffBytes {
		return diff, false
	}
	cut := diff[:maxPromptDiffBytes]
	if i := strings.LastIndex(cut, "\ndiff --git "); i > 0 {
		cut = cut[:i]
	}
	return cut + "\n\n[diff truncated]\n", true
}

// GenerateWalkthrough builds a review tour over one scope: "worktree",
// "staged", "all", or "branch" (everything since the fork point from base).
func (s *Service) GenerateWalkthrough(scope, base, model string) (Walkthrough, error) {
	if scope == "" {
		scope = "all"
	}
	diff, err := s.GitDiffScope(scope, base)
	if err != nil {
		return Walkthrough{}, err
	}
	if strings.TrimSpace(diff) == "" {
		return Walkthrough{}, errors.New("no changes in this scope")
	}
	body, truncated := truncateDiff(diff)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	raw, err := s.complete(ctx, "slow", model, walkthroughSystemPrompt, "Diff:\n\n"+body)
	if err != nil {
		return Walkthrough{}, err
	}
	walk, err := parseWalkthrough(raw)
	if err != nil {
		return Walkthrough{}, err
	}
	walk.Scope = scope
	walk.Base = base
	walk.Model = model
	walk.Truncated = truncated
	walk.Fingerprint = fingerprint(diff)
	walk.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	// Coverage is computed here rather than trusted from the model: a stop it
	// forgot to write is exactly the change a reviewer must not miss.
	walk.Uncovered = uncoveredFiles(diff, walk.Stops)
	return walk, nil
}

// WalkthroughStale reports whether the scope's diff has changed since a
// walkthrough was generated from it.
func (s *Service) WalkthroughStale(scope, base, fingerprint_ string) (bool, error) {
	diff, err := s.GitDiffScope(scope, base)
	if err != nil {
		return false, err
	}
	return fingerprint(diff) != fingerprint_, nil
}

func fingerprint(diff string) string {
	sum := sha256.Sum256([]byte(diff))
	return hex.EncodeToString(sum[:8])
}

// parseWalkthrough tolerates the code fence and the leading prose that models
// add around JSON they were asked to emit bare.
func parseWalkthrough(raw string) (Walkthrough, error) {
	text := strings.TrimSpace(raw)
	if i := strings.Index(text, "{"); i > 0 {
		text = text[i:]
	}
	if j := strings.LastIndex(text, "}"); j >= 0 {
		text = text[:j+1]
	}
	var walk Walkthrough
	if err := json.Unmarshal([]byte(text), &walk); err != nil {
		return Walkthrough{}, fmt.Errorf("the model did not return a walkthrough: %w", err)
	}
	if len(walk.Stops) == 0 {
		return Walkthrough{}, errors.New("the model returned no walkthrough stops")
	}
	for i := range walk.Stops {
		if walk.Stops[i].Files == nil {
			walk.Stops[i].Files = []string{}
		}
		tag := strings.ToLower(strings.TrimSpace(walk.Stops[i].Tag))
		if tag != "key" && tag != "context" {
			tag = ""
		}
		walk.Stops[i].Tag = tag
	}
	return walk, nil
}

// uncoveredFiles lists changed paths no stop mentions.
func uncoveredFiles(diff string, stops []WalkStop) []string {
	covered := map[string]bool{}
	for _, stop := range stops {
		for _, f := range stop.Files {
			covered[strings.TrimSpace(f)] = true
		}
	}
	missing := []string{}
	for _, path := range diffPaths(diff) {
		if !covered[path] {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)
	return missing
}

// diffPaths pulls the new-side path out of every "diff --git a/x b/y" header.
func diffPaths(diff string) []string {
	seen := map[string]bool{}
	paths := []string{}
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "diff --git ") {
			continue
		}
		rest := strings.TrimPrefix(line, "diff --git ")
		i := strings.Index(rest, " b/")
		if i < 0 {
			continue
		}
		path := strings.TrimPrefix(rest[i+1:], "b/")
		if path != "" && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths
}
