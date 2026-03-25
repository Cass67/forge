package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/agent/tools"
)

func TestClassifyTaskProfileDetectsFocusedLanguageReview(t *testing.T) {
	task := "TASK: Inspect the repository's Python files and identify cleanup opportunities, code smells, outdated patterns, risky constructs, or maintainability issues. OUTCOME: A concrete list of findings with file paths and brief rationale."

	profile := classifyTaskProfile(task)

	if profile.Scope != taskScopeFocusedFiles {
		t.Fatalf("expected focused-files scope, got %#v", profile)
	}
	if profile.TargetLang != "python" {
		t.Fatalf("expected python target lang, got %#v", profile)
	}
	if profile.TargetGlob != "**/*.py" {
		t.Fatalf("expected python target glob, got %#v", profile)
	}
	if profile.EvidenceMinReads != 3 {
		t.Fatalf("expected default evidence min reads 3, got %#v", profile)
	}
	if profile.Topic != "code-quality" {
		t.Fatalf("expected code-quality topic, got %#v", profile)
	}
}

func TestClassifyTaskProfileDetectsFocusedExtensionReview(t *testing.T) {
	task := "TASK: Review the repo's .sql files for cleanup opportunities and maintainability issues."

	profile := classifyTaskProfile(task)

	if profile.Scope != taskScopeFocusedFiles {
		t.Fatalf("expected focused-files scope, got %#v", profile)
	}
	if profile.TargetGlob != "**/*.sql" {
		t.Fatalf("expected sql target glob, got %#v", profile)
	}
	if profile.EvidenceMinReads != 3 {
		t.Fatalf("expected default evidence min reads 3, got %#v", profile)
	}
}

func TestClassifyDelegatedTaskProfilePrefersFocusedUserScopeOverRepoReviewWording(t *testing.T) {
	userMessage := "take a look over this repo, look at the py files and let me know if there is anything that should be cleaned up or changed"
	task := "TASK: Inspect the repository's Python files and identify cleanup opportunities, code smells, outdated patterns, risky constructs, or maintainability issues. OUTCOME: Evidence-backed findings only. Gather repository purpose, structure, tech stack, key modules, and concrete maintenance signals with file/path references so an architect can synthesize recommendations."

	profile := classifyDelegatedTaskProfile(userMessage, task)

	if profile.Scope != taskScopeFocusedFiles {
		t.Fatalf("expected focused-files scope, got %#v", profile)
	}
	if profile.TargetLang != "python" {
		t.Fatalf("expected python target lang, got %#v", profile)
	}
	if profile.TargetGlob != "**/*.py" {
		t.Fatalf("expected python target glob, got %#v", profile)
	}
}

func TestClassifyTaskProfileDetectsRepoReviewAuditPrompt(t *testing.T) {
	task := "audit the repo for problems"

	profile := classifyTaskProfile(task)

	if profile.Scope != taskScopeRepoReview {
		t.Fatalf("expected repo-review scope, got %#v", profile)
	}
}

func TestClassifyDelegatedTaskProfileKeepsRepoReviewForArchitectSynthesisTask(t *testing.T) {
	userMessage := "audit the repo for problems"
	task := "Synthesize these evidence-backed findings into repo-review recommendations and risk prioritization."

	profile := classifyDelegatedTaskProfile(userMessage, task)

	if profile.Scope != taskScopeRepoReview {
		t.Fatalf("expected repo-review scope, got %#v", profile)
	}
}

func TestClassifyTaskProfileDoesNotTreatNarrowRepositoryTraceAsRepoReview(t *testing.T) {
	task := "TASK: Search the repository for the alert source. OUTCOME: Evidence-backed findings only. MUST NOT: Do not speculate."

	profile := classifyTaskProfile(task)

	if profile.Scope == taskScopeRepoReview {
		t.Fatalf("expected non-repo-review scope for narrow trace task, got %#v", profile)
	}
}

func TestClassifyTaskProfileKeepsExplicitRepoReviewScopeOverLanguageHints(t *testing.T) {
	task := strings.Join([]string{
		"TASK: Review the repository code quality with concrete evidence from the code, focusing on representative shell files across the main areas.",
		"OUTCOME: A code-focused assessment grounded in files actually read.",
		"CONTEXT: Read representative shell files if present; include file paths in findings.",
		"SCOPE: repo-review",
		"TOPIC: code-quality",
		"EVIDENCE_MIN_READS: 10",
		"MUST NOT: Do not modify files.",
	}, "\n")

	profile := classifyTaskProfile(task)

	if profile.Scope != taskScopeRepoReview {
		t.Fatalf("expected explicit repo-review scope to win, got %#v", profile)
	}
	if profile.TargetLang != "" {
		t.Fatalf("expected repo-review scope to clear target lang, got %#v", profile)
	}
	if profile.TargetGlob != "" {
		t.Fatalf("expected repo-review scope to clear target glob, got %#v", profile)
	}
	if profile.EvidenceMinReads != 0 {
		t.Fatalf("expected repo-review scope to clear evidence minimum, got %#v", profile)
	}
}

func TestRewriteDispatchScoutTaskAddsFocusedFileScopeMetadata(t *testing.T) {
	task := "TASK: Inspect the repository's Python files and identify cleanup opportunities, code smells, outdated patterns, risky constructs, or maintainability issues. OUTCOME: A concrete list of findings with file paths and brief rationale."

	rewritten := rewriteDispatchScoutTask(task)

	if !strings.Contains(rewritten, "\nSCOPE: focused-files") {
		t.Fatalf("expected focused-files scope metadata, got %q", rewritten)
	}
	if !strings.Contains(rewritten, "\nTARGET_LANG: python") {
		t.Fatalf("expected python target lang metadata, got %q", rewritten)
	}
	if !strings.Contains(rewritten, "\nTARGET_GLOB: **/*.py") {
		t.Fatalf("expected python target glob metadata, got %q", rewritten)
	}
	if !strings.Contains(rewritten, "\nEVIDENCE_MIN_READS: 3") {
		t.Fatalf("expected evidence minimum metadata, got %q", rewritten)
	}
}

func TestRewriteDispatchScoutTaskRemovesFocusedSelectorsFromRepoReviewTask(t *testing.T) {
	task := strings.Join([]string{
		"TASK: Review the repository code quality with concrete evidence from the code, focusing on representative shell files across the main areas.",
		"OUTCOME: A code-focused assessment grounded in files actually read.",
		"CONTEXT: Read representative shell files if present; include file paths in findings.",
		"MUST NOT: Do not modify files.",
		"SCOPE: repo-review",
		"TARGET_LANG: shell",
		"TARGET_GLOB: **/*.sh",
		"EVIDENCE_MIN_READS: 10",
		"TOPIC: code-quality",
	}, "\n")

	rewritten := rewriteDispatchScoutTask(task)

	if !strings.Contains(rewritten, "\nSCOPE: repo-review") {
		t.Fatalf("expected repo-review scope metadata, got %q", rewritten)
	}
	for _, forbidden := range []string{"TARGET_LANG:", "TARGET_GLOB:", "EVIDENCE_MIN_READS:"} {
		if strings.Contains(rewritten, forbidden) {
			t.Fatalf("repo-review scout task should not keep focused-file selectors, got %q", rewritten)
		}
	}
}

func TestScoutRunFocusedFilesScopeReadsMultipleMatchingFilesBeforeStopping(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha.py", "beta.py", "gamma.py"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("print('ok')\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"glob\", \"args\": {\"pattern\": \"**/*.py\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"alpha.py\"}}\n</tool_call>",
		`{"status":"complete","message":"I have enough evidence.","artifact_kind":"evidence","artifact":"Only read one file.","next_role":"","next_task":""}`,
		"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"beta.py\"}}\n</tool_call>",
		`{"status":"complete","message":"I have enough evidence.","artifact_kind":"evidence","artifact":"Only read two files.","next_role":"","next_task":""}`,
		"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"gamma.py\"}}\n</tool_call>",
		`{"status":"complete","message":"Summary ready.","artifact_kind":"evidence","artifact":"Read three matching Python files before concluding.","next_role":"","next_task":""}`,
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.NewGlob(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	a.SetRole("scout")
	a.isSubAgent = true

	task := strings.Join([]string{
		"TASK: Inspect the repository's Python files and identify cleanup opportunities, code smells, outdated patterns, risky constructs, or maintainability issues.",
		"OUTCOME: Evidence-backed findings only.",
		"MUST NOT: Do not modify files.",
		"SCOPE: focused-files",
		"TARGET_LANG: python",
		"TARGET_GLOB: **/*.py",
		"EVIDENCE_MIN_READS: 3",
	}, "\n")

	if err := a.Run(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 7 {
		t.Fatalf("expected scout to keep going until it read three matching files, got %d calls", driver.callCount)
	}

	joinedHistory := make([]string, 0, len(a.history))
	for _, msg := range a.history {
		joinedHistory = append(joinedHistory, msg.Content)
	}
	joined := strings.Join(joinedHistory, "\n")
	if strings.Contains(joined, "Repo-review evidence is still incomplete") {
		t.Fatalf("focused-files task should not receive repo-review nudges: %s", joined)
	}
	if strings.Count(joined, "Focused-file evidence is still incomplete") != 2 {
		t.Fatalf("expected two focused-files nudges after premature summaries, got history: %s", joined)
	}
}
