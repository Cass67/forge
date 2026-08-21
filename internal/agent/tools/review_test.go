package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// branchWithCommit puts one commit on a feature branch so the review has a
// merge-base that is behind HEAD.
func branchWithCommit(t *testing.T) string {
	t.Helper()
	dir := initGitRepo(t)
	runGit(t, dir, "checkout", "-b", "feature")
	mustWriteFile(t, filepath.Join(dir, "feature.go"), "package main\n\nfunc Feature() {}\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "add feature")
	return dir
}

func runTool(t *testing.T, tool Tool, args map[string]any) string {
	t.Helper()
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("%s: %v", tool.Name, err)
	}
	return out
}

func TestReviewDiffUsesMergeBaseNotBaseTip(t *testing.T) {
	dir := branchWithCommit(t)
	// A commit lands on main after the branch forked. It must not appear as a
	// change under review, which is exactly what diffing against main's tip
	// would do.
	runGit(t, dir, "checkout", "main")
	mustWriteFile(t, filepath.Join(dir, "unrelated.go"), "package main\n\nfunc Unrelated() {}\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "unrelated work on main")
	runGit(t, dir, "checkout", "feature")

	out := runTool(t, NewReviewDiff(dir), map[string]any{})
	if !strings.Contains(out, "feature.go") {
		t.Fatalf("branch change missing from review:\n%s", out)
	}
	if strings.Contains(out, "unrelated.go") {
		t.Fatalf("base-branch commit leaked into the review:\n%s", out)
	}
	if !strings.Contains(out, "add feature") {
		t.Fatalf("commits under review missing:\n%s", out)
	}
}

func TestReviewDiffIncludesUncommittedWork(t *testing.T) {
	dir := branchWithCommit(t)
	mustWriteFile(t, filepath.Join(dir, "feature.go"), "package main\n\nfunc Feature() int { return 1 }\n")
	mustWriteFile(t, filepath.Join(dir, "brand_new.go"), "package main\n")

	out := runTool(t, NewReviewDiff(dir), map[string]any{})
	if !strings.Contains(out, "return 1") {
		t.Fatalf("uncommitted edit missing from patch:\n%s", out)
	}
	if !strings.Contains(out, "brand_new.go") {
		t.Fatalf("untracked file not reported:\n%s", out)
	}
}

func TestReviewDiffOnBaseBranchFallsBackToWorkingTree(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")

	out := runTool(t, NewReviewDiff(dir), map[string]any{})
	if !strings.Contains(out, "no commits of its own") && !strings.Contains(out, "no base branch found") {
		t.Fatalf("expected a working-tree fallback note:\n%s", out)
	}
	if !strings.Contains(out, "func main()") {
		t.Fatalf("working tree change missing:\n%s", out)
	}
}

func TestReviewDiffRejectsUnknownBase(t *testing.T) {
	dir := branchWithCommit(t)
	out := runTool(t, NewReviewDiff(dir), map[string]any{"base": "nope/missing"})
	if !strings.Contains(out, "does not exist") {
		t.Fatalf("expected an unknown-base error, got:\n%s", out)
	}
}

func TestReviewDiffStatOnlyOmitsPatch(t *testing.T) {
	dir := branchWithCommit(t)
	out := runTool(t, NewReviewDiff(dir), map[string]any{"stat": true})
	if !strings.Contains(out, "feature.go") {
		t.Fatalf("file stat missing:\n%s", out)
	}
	if strings.Contains(out, "patch:") {
		t.Fatalf("stat=true still returned a patch:\n%s", out)
	}
}

func TestReviewDiffScopedToPath(t *testing.T) {
	dir := branchWithCommit(t)
	mustWriteFile(t, filepath.Join(dir, "other.go"), "package main\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "second file")

	out := runTool(t, NewReviewDiff(dir), map[string]any{"path": "feature.go"})
	if !strings.Contains(out, "feature.go") || strings.Contains(out, "other.go") {
		t.Fatalf("path scope not applied:\n%s", out)
	}
}

func TestReviewDiffRedactsSecrets(t *testing.T) {
	dir := branchWithCommit(t)
	mustWriteFile(t, filepath.Join(dir, "config.go"), "package main\n\nconst key = \"sk-ant-api03-"+strings.Repeat("a", 40)+"\"\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "add config")

	out := runTool(t, NewReviewDiff(dir), map[string]any{})
	if !strings.Contains(out, "config.go") {
		t.Fatalf("the file under test never reached the patch:\n%s", out)
	}
	if strings.Contains(out, "sk-ant-api03-"+strings.Repeat("a", 40)) {
		t.Fatal("review patch leaked a secret verbatim")
	}
}

func TestReportFindingsSortsBySeverity(t *testing.T) {
	out := runTool(t, NewReportFindings(), map[string]any{
		"verdict": "one real bug",
		"findings": []any{
			map[string]any{"file": "a.go", "severity": "low", "summary": "nit", "detail": "small"},
			map[string]any{"file": "b.go", "line": float64(12), "severity": "critical", "category": "correctness", "summary": "nil deref", "detail": "b(nil) panics"},
		},
	})
	critical := strings.Index(out, "CRITICAL")
	low := strings.Index(out, "LOW")
	if critical < 0 || low < 0 || critical > low {
		t.Fatalf("findings not ranked most severe first:\n%s", out)
	}
	if !strings.Contains(out, "b.go:12") {
		t.Fatalf("line anchor missing:\n%s", out)
	}
	if !strings.Contains(out, "one real bug") {
		t.Fatalf("verdict missing:\n%s", out)
	}
}

func TestReportFindingsEmptyIsClean(t *testing.T) {
	out := runTool(t, NewReportFindings(), map[string]any{"findings": []any{}})
	if !strings.Contains(out, "no findings") {
		t.Fatalf("expected a clean result, got:\n%s", out)
	}
}

func TestReportFindingsRejectsIncompleteFinding(t *testing.T) {
	cases := []struct {
		name    string
		finding map[string]any
	}{
		{"no detail", map[string]any{"file": "a.go", "severity": "high", "summary": "bad"}},
		{"no file", map[string]any{"severity": "high", "summary": "bad", "detail": "boom"}},
		{"bad severity", map[string]any{"file": "a.go", "severity": "nit", "summary": "bad", "detail": "boom"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewReportFindings().Execute(context.Background(), map[string]any{"findings": []any{tc.finding}}); err == nil {
				t.Fatal("expected the finding to be rejected")
			}
		})
	}
}

func TestReportFindingsRequiresFindingsKey(t *testing.T) {
	if _, err := NewReportFindings().Execute(context.Background(), map[string]any{}); err == nil {
		t.Fatal("expected missing findings to be an error")
	}
}

func TestReviewPromptNamesTheBase(t *testing.T) {
	if got := ReviewPromptFor(""); strings.Contains(got, "base=") {
		t.Fatalf("bare /review should not pin a base:\n%s", got)
	}
	got := ReviewPromptFor("develop")
	if !strings.Contains(got, `base="develop"`) || !strings.Contains(got, "against develop") {
		t.Fatalf("explicit base missing from prompt:\n%s", got)
	}
}

func TestReviewDiffOutsideRepoErrors(t *testing.T) {
	dir := t.TempDir()
	out := runTool(t, NewReviewDiff(dir), map[string]any{})
	if !strings.HasPrefix(out, "error:") {
		t.Fatalf("expected an error outside a repository, got:\n%s", out)
	}
}
