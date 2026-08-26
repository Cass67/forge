package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"forge/internal/llm"
)

// reviewBaseCandidates are tried in order when the caller does not name a base.
// origin/HEAD is what the remote says its default branch is, which beats
// guessing; the literal names cover repositories with no remote at all.
var reviewBaseCandidates = []string{"origin/HEAD", "origin/main", "main", "origin/master", "master"}

func NewReviewDiff(workDir string) Tool {
	return NewReviewDiffWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir))
}

func NewReviewDiffWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider) Tool {
	secretPolicy := DefaultSecretPolicy()
	return Tool{
		Name:        "review_diff",
		Description: "Assemble everything a code review needs: the base it is reviewing against, the commits on this branch, the changed files, and the full patch from the merge-base to the working tree (committed and uncommitted). Prefer this over git_diff when reviewing a branch, because it diffs from the merge-base rather than the base branch tip, so unrelated commits on the base do not show up as changes.",
		Parameters: []ParameterDef{
			{Name: "base", Type: "string", Description: "branch or ref to review against (default: auto-detected default branch)", Required: false},
			{Name: "path", Type: "string", Description: "optional repository-relative path to limit the review to", Required: false},
			{Name: "stat", Type: "bool", Description: "return only the summary and file stat, without the patch (default false)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			workDir := currentWorkDir(provider, fallbackWorkDir)
			scope := strings.TrimSpace(stringArg(args, "path"))
			target, err := resolveReviewTarget(ctx, workDir, strings.TrimSpace(stringArg(args, "base")))
			if err != nil {
				return fmt.Sprintf("error: %s", err), nil
			}
			statOnly, _ := args["stat"].(bool)
			out := renderReviewTarget(ctx, workDir, target, scope, statOnly)
			result, _ := secretPolicy.ApplyCommandOutput(out)
			return truncateGitOutput(result), nil
		},
	}
}

type reviewTarget struct {
	base      string
	mergeBase string
	head      string
	branch    string
	// noBase is set when nothing resolved as a base branch, or when the branch
	// has no commits of its own. The review is then of the working tree alone.
	noBase bool
	note   string
}

func resolveReviewTarget(ctx context.Context, workDir, requestedBase string) (reviewTarget, error) {
	head, err := gitOutput(ctx, workDir, "git", "rev-parse", "HEAD")
	if err != nil {
		return reviewTarget{}, fmt.Errorf("not a git repository with commits: %v", err)
	}
	branch, _ := gitOutput(ctx, workDir, "git", "branch", "--show-current")
	target := reviewTarget{
		head:   strings.TrimSpace(head),
		branch: strings.TrimSpace(branch),
	}

	base := requestedBase
	if base == "" {
		for _, candidate := range reviewBaseCandidates {
			if gitRefExists(ctx, workDir, candidate) {
				base = candidate
				break
			}
		}
	} else if !gitRefExists(ctx, workDir, base) {
		return reviewTarget{}, fmt.Errorf("base ref %q does not exist", base)
	}
	if base == "" {
		target.noBase = true
		target.mergeBase = "HEAD"
		target.note = "no base branch found; reviewing uncommitted changes against HEAD"
		return target, nil
	}

	mergeBase, err := gitOutput(ctx, workDir, "git", "merge-base", base, "HEAD")
	if err != nil {
		target.noBase = true
		target.mergeBase = "HEAD"
		target.base = base
		target.note = fmt.Sprintf("%s shares no history with HEAD; reviewing uncommitted changes against HEAD", base)
		return target, nil
	}
	target.base = base
	target.mergeBase = strings.TrimSpace(mergeBase)
	if target.mergeBase == target.head {
		target.noBase = true
		target.mergeBase = "HEAD"
		target.note = fmt.Sprintf("HEAD is already contained in %s, so the branch has no commits of its own; reviewing uncommitted changes against HEAD", base)
	}
	return target, nil
}

func renderReviewTarget(ctx context.Context, workDir string, target reviewTarget, scope string, statOnly bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "base: %s\n", emptyToUnknown(target.base))
	fmt.Fprintf(&sb, "merge_base: %s\n", target.mergeBase)
	fmt.Fprintf(&sb, "head: %s\n", target.head)
	fmt.Fprintf(&sb, "branch: %s\n", emptyToUnknown(target.branch))
	if scope != "" {
		fmt.Fprintf(&sb, "scoped_to: %s\n", scope)
	}
	if target.note != "" {
		fmt.Fprintf(&sb, "note: %s\n", target.note)
	}

	if !target.noBase {
		commits, err := gitOutput(ctx, workDir, "git", "log", "--oneline", target.mergeBase+"..HEAD")
		if commits = strings.TrimSpace(commits); err == nil && commits != "" {
			sb.WriteString("\ncommits under review:\n")
			sb.WriteString(commits)
			sb.WriteString("\n")
		}
	}

	scopeArgs := []string{}
	if scope != "" {
		scopeArgs = []string{"--", scope}
	}
	diffArgs := append([]string{"git", "diff", target.mergeBase}, scopeArgs...)
	statArgs := append([]string{"git", "diff", "--stat", target.mergeBase}, scopeArgs...)

	stat, err := gitOutput(ctx, workDir, statArgs...)
	if err == nil && strings.TrimSpace(stat) != "" {
		sb.WriteString("\nchanged files:\n")
		sb.WriteString(strings.Trim(stat, "\n"))
		sb.WriteString("\n")
	} else {
		sb.WriteString("\nchanged files: none\n")
	}

	if untracked, err := gitNulPaths(ctx, workDir, "ls-files", "--others", "--exclude-standard", "-z"); err == nil && len(untracked) > 0 {
		sb.WriteString("\nuntracked files (not in the patch below; read them directly):\n")
		sb.WriteString(strings.Join(untracked, "\n"))
		sb.WriteString("\n")
	}

	if statOnly {
		return sb.String()
	}
	patch, err := gitOutput(ctx, workDir, diffArgs...)
	if err != nil {
		fmt.Fprintf(&sb, "\nerror generating patch: %s\n", err)
		return sb.String()
	}
	if strings.TrimSpace(patch) == "" {
		sb.WriteString("\npatch: empty\n")
		return sb.String()
	}
	sb.WriteString("\npatch:\n")
	sb.WriteString(patch)
	return sb.String()
}

type reviewFinding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Category string `json:"category"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail"`
}

var reviewSeverityRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}

func NewReportFindings() Tool {
	additional := false
	return Tool{
		Name:        "report_findings",
		Description: "Report the results of a code review as a ranked list. Call it exactly once, at the end of a review, with the findings that survived verification — an empty list is the correct call when the change is clean. Every finding needs a concrete failure the reader can check, not a style preference.",
		Schema: &llm.ToolSchema{
			Type: "object",
			Properties: map[string]*llm.ToolSchema{
				"findings": {
					Type:        "array",
					Description: "verified findings, most severe first",
					Items: &llm.ToolSchema{
						Type: "object",
						Properties: map[string]*llm.ToolSchema{
							"file":     {Type: "string", Description: "repository-relative path"},
							"line":     {Type: "integer", Description: "1-indexed line the finding anchors to"},
							"severity": {Type: "string", Enum: []string{"critical", "high", "medium", "low"}},
							"category": {Type: "string", Description: "short slug, e.g. correctness, security, simplification, efficiency, test-coverage"},
							"summary":  {Type: "string", Description: "one sentence stating the defect"},
							"detail":   {Type: "string", Description: "the concrete failure: inputs or state, then the wrong result"},
						},
						Required:             []string{"file", "severity", "summary", "detail"},
						AdditionalProperties: &additional,
					},
				},
				"verdict": {Type: "string", Description: "one-line overall assessment of the change"},
			},
			Required:             []string{"findings"},
			AdditionalProperties: &additional,
		},
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			findings, err := reviewFindingsFromArgs(args)
			if err != nil {
				return "", err
			}
			for i := range findings {
				findings[i].File = strings.TrimSpace(findings[i].File)
				findings[i].Summary = strings.TrimSpace(findings[i].Summary)
				findings[i].Detail = strings.TrimSpace(findings[i].Detail)
				findings[i].Category = strings.TrimSpace(findings[i].Category)
				findings[i].Severity = strings.ToLower(strings.TrimSpace(findings[i].Severity))
				if findings[i].File == "" {
					return "", fmt.Errorf("finding %d is missing file", i+1)
				}
				if findings[i].Summary == "" {
					return "", fmt.Errorf("finding %d is missing summary", i+1)
				}
				if findings[i].Detail == "" {
					return "", fmt.Errorf("finding %d is missing detail; state the concrete failure, not a preference", i+1)
				}
				if _, ok := reviewSeverityRank[findings[i].Severity]; !ok {
					return "", fmt.Errorf("finding %d has invalid severity %q; use critical, high, medium, or low", i+1, findings[i].Severity)
				}
			}
			slices.SortStableFunc(findings, func(a, b reviewFinding) int {
				return cmp.Compare(reviewSeverityRank[a.Severity], reviewSeverityRank[b.Severity])
			})
			return renderReviewFindings(findings, strings.TrimSpace(stringArg(args, "verdict"))), nil
		},
	}
}

func reviewFindingsFromArgs(args map[string]any) ([]reviewFinding, error) {
	raw, ok := args["findings"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("findings is required; pass an empty list when the change is clean")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid findings: %w", err)
	}
	var findings []reviewFinding
	if err := json.Unmarshal(data, &findings); err != nil {
		return nil, fmt.Errorf("invalid findings: %w", err)
	}
	return findings, nil
}

func renderReviewFindings(findings []reviewFinding, verdict string) string {
	var sb strings.Builder
	if len(findings) == 0 {
		sb.WriteString("Review complete: no findings.\n")
		if verdict != "" {
			sb.WriteString("\n" + verdict + "\n")
		}
		return sb.String()
	}
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Severity]++
	}
	var parts []string
	for _, severity := range []string{"critical", "high", "medium", "low"} {
		if counts[severity] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[severity], severity))
		}
	}
	fmt.Fprintf(&sb, "Review complete: %d finding(s) — %s\n", len(findings), strings.Join(parts, ", "))
	if verdict != "" {
		sb.WriteString("\n" + verdict + "\n")
	}
	for i, f := range findings {
		location := f.File
		if f.Line > 0 {
			location = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		category := ""
		if f.Category != "" {
			category = " [" + f.Category + "]"
		}
		fmt.Fprintf(&sb, "\n%d. %s%s %s\n   %s\n   %s\n", i+1, strings.ToUpper(f.Severity), category, location, f.Summary, f.Detail)
	}
	return sb.String()
}

// ReviewPromptFor is the turn text /review submits. It lives here rather than
// in the TUI so both surfaces send the same instructions.
func ReviewPromptFor(base string) string {
	target := "the current branch"
	baseArg := ""
	if base = strings.TrimSpace(base); base != "" {
		target = "the current branch against " + base
		baseArg = fmt.Sprintf(" with base=%q", base)
	}
	return fmt.Sprintf(`Review %s for defects.

1. Call review_diff%s to get the base, the commits, and the patch. Do not use git_diff for this.
2. Read the surrounding code for every changed region — a patch alone does not show whether a caller, a test, or an invariant elsewhere is broken by it. Use git_blame and git_log when the intent of the code being changed is unclear.
3. Verify each candidate finding against the actual code before reporting it. Discard anything you cannot tie to a concrete failure.
4. Call report_findings once with what survived, most severe first, and an empty list if the change is clean.

Report defects: correctness, security, data loss, broken invariants, missing test coverage for new behaviour, and genuine simplifications. Do not report formatting or style preferences.`, target, baseArg)
}
