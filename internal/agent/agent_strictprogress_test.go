package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/agent/tools"
	"forge/internal/llm"
)

func TestStrictInspectRepeatedReadOnlyToolCallAfterOneDifferentStepGetsNoProgressNudge(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# sandbox\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "app.js"), []byte("export const app = 1;\n"), 0o644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			nil,
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				lower := strings.ToLower(joined)
				if !strings.Contains(lower, "same result") && !strings.Contains(lower, "no progress") {
					return fmt.Errorf("expected repeated-read no-progress nudge after one different step, got %q", joined)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\"src\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\"src\"}}\n</tool_call>",
			"This directory has a README plus one source file under src/.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 8, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: directory
This is a read-only inspection turn.
USER REQUEST:
describe this directory and keep me updated as you go`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 4 {
		t.Fatalf("expected strict inspect run to recover after no-progress nudge, got %d driver calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "This directory has a README plus one source file under src/.") {
		t.Fatalf("final output missing completion text: %q", output.String())
	}
}

func TestStrictInspectDirectoryEnoughEvidenceNudgesAnswerInsteadOfMoreTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# sandbox\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "app.js"), []byte("export const app = 1;\n"), 0o644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				lower := strings.ToLower(joined)
				if !strings.Contains(lower, "enough evidence") || !strings.Contains(lower, "answer now") {
					return fmt.Errorf("expected enough-evidence answer nudge, got %q", joined)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\"src\"}}\n</tool_call>",
			"This directory contains a README at the root and a single app.js file under src/.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 8, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: directory
This is a read-only inspection turn.
USER REQUEST:
describe this directory and keep me updated as you go`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 3 {
		t.Fatalf("expected strict inspect run to answer once enough evidence was gathered, got %d driver calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "This directory contains a README at the root and a single app.js file under src/.") {
		t.Fatalf("final output missing completion text: %q", output.String())
	}
}

func TestStrictInspectDirectoryEarlyAnswerRetriesUntilRepresentativeDetail(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# sandbox\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "app.js"), []byte("export const app = 1;\n"), 0o644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			nil,
			func(messages []llm.Message) error {
				if len(messages) == 0 {
					return fmt.Errorf("missing control history")
				}
				last := messages[len(messages)-1].Content
				lower := strings.ToLower(last)
				if !strings.Contains(lower, "directory walkthrough evidence is still incomplete") || !strings.Contains(lower, "representative child file or subdirectory") {
					return fmt.Errorf("expected directory retry nudge after early answer, got %q", last)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
			"The directory has a README and a src folder.",
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\"src\"}}\n</tool_call>",
			"This directory contains a README at the root and a single app.js file under src/.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 8, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: directory
This is a read-only inspection turn.
USER REQUEST:
describe this directory and keep me updated as you go`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 4 {
		t.Fatalf("expected directory inspect run to reject the early answer and continue inspecting, got %d driver calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "This directory contains a README at the root and a single app.js file under src/.") {
		t.Fatalf("final output missing completion text: %q", output.String())
	}
}

func TestStrictInspectRepositoryEnoughEvidenceNudgesAnswerInsteadOfMoreTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "forge"), 0o755); err != nil {
		t.Fatalf("mkdir cmd/forge: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Forge\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module forge\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "forge", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "agent.go"), []byte("package agent\n"), 0o644); err != nil {
		t.Fatalf("write agent.go: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			nil,
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				lower := strings.ToLower(joined)
				if !strings.Contains(lower, "enough evidence") || !strings.Contains(lower, "repo tour") || !strings.Contains(lower, "answer now") {
					return fmt.Errorf("expected repository enough-evidence answer nudge, got %q", joined)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"cmd/forge/main.go\"}}\n</tool_call>",
			"This repo is a Go terminal-first coding agent with docs at the root, a cmd entrypoint, and most implementation under internal/.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: repository
This is a read-only inspection turn.
USER REQUEST:
give me a quick repo tour and keep me updated as you go`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 4 {
		t.Fatalf("expected strict repository inspect run to answer once repo-tour evidence was gathered, got %d driver calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "This repo is a Go terminal-first coding agent with docs at the root, a cmd entrypoint, and most implementation under internal/.") {
		t.Fatalf("final output missing completion text: %q", output.String())
	}
}

func TestStrictInspectRepositoryRequiresRepresentativeSourceFileReadBeforeAnswering(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "forge"), 0o755); err != nil {
		t.Fatalf("mkdir cmd/forge: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Forge\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module forge\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "forge", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "agent.go"), []byte("package agent\n"), 0o644); err != nil {
		t.Fatalf("write agent.go: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			nil,
			nil,
			func(messages []llm.Message) error {
				if len(messages) == 0 {
					return fmt.Errorf("missing control history")
				}
				last := messages[len(messages)-1].Content
				lower := strings.ToLower(last)
				if !strings.Contains(lower, "repo-tour evidence is still incomplete") || !strings.Contains(lower, "representative implementation file") || !strings.Contains(last, "cmd") {
					return fmt.Errorf("expected representative source-file retry nudge after directory-only source evidence, got %q", last)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\"cmd\"}}\n</tool_call>",
			"This repo is a Go terminal-first coding agent with docs at the root and code under cmd/ and internal/.",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"cmd/forge/main.go\"}}\n</tool_call>",
			"This repo is a Go terminal-first coding agent with docs at the root, a cmd entrypoint, and most implementation under internal/.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: repository
This is a read-only inspection turn.
USER REQUEST:
give me a quick repo tour and keep me updated as you go`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 6 {
		t.Fatalf("expected repository inspect run to reject directory-only source evidence and continue until a representative source file was read, got %d calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "This repo is a Go terminal-first coding agent with docs at the root, a cmd entrypoint, and most implementation under internal/.") {
		t.Fatalf("final output missing completion text: %q", output.String())
	}
}

func TestInspectRepositoryQuickTourNudgePrefersObservedSourceFileHint(t *testing.T) {
	state := newInspectRepositoryEvidenceState("/repo", strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: repository
This is a read-only inspection turn.
USER REQUEST:
explain how the harness routes preview follow-ups in this repo`))

	state.Observe(ToolCall{Name: "search", Args: map[string]any{"pattern": "preview"}}, "./internal/harness/strictlocal_test.go:65: preview_server_ensure")
	state.Observe(ToolCall{Name: "list_dir", Args: map[string]any{"path": "."}}, "README.md\ncmd/\ninternal/\n")

	msg := state.QuickTourNudgeMessage()
	if !strings.Contains(msg, "internal/harness/strictlocal.go") {
		t.Fatalf("expected quick-tour nudge to prefer observed source-file hint, got %q", msg)
	}
	if strings.Contains(msg, " under cmd") {
		t.Fatalf("unexpected generic cmd source target in quick-tour nudge: %q", msg)
	}
}

func TestInspectRepositorySourceInspectionTargetPrefersRequestAlignedHarnessFileAfterDirectoryListing(t *testing.T) {
	state := newInspectRepositoryEvidenceState("/repo", strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: repository
This is a read-only inspection turn.
USER REQUEST:
explain how the harness routes preview follow-ups in this repo`))

	state.Observe(ToolCall{Name: "search", Args: map[string]any{"pattern": "preview"}}, strings.Join([]string{
		"./internal/agent/progress.go:28: case \"preview_server_ensure\":",
		"./internal/harness/strictlocal_test.go:65: preview_server_ensure",
	}, "\n"))
	state.Observe(ToolCall{Name: "list_dir", Args: map[string]any{"path": "."}}, "README.md\ncmd/\ninternal/\n")
	state.Observe(ToolCall{Name: "list_dir", Args: map[string]any{"path": "internal/harness"}}, "classifier.go\nplanner.go\npolicy.go\nstrictlocal.go\nthread.go\n")

	if got := state.sourceInspectionTarget(); !strings.HasPrefix(got, "internal/harness/") {
		t.Fatalf("expected request-aligned harness source target, got %q", got)
	}
}

func TestInspectRepositoryImplementationGroundedQuestionNeedsRelevantSourceRead(t *testing.T) {
	state := newInspectRepositoryEvidenceState("/repo", strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: repository
This is a read-only inspection turn.
USER REQUEST:
explain how the harness routes preview follow-ups in this repo`))

	state.Observe(ToolCall{Name: "search", Args: map[string]any{"pattern": "preview"}}, strings.Join([]string{
		"./internal/agent/progress.go:28: case \"preview_server_ensure\":",
		"./internal/harness/strictlocal_test.go:65: preview_server_ensure",
	}, "\n"))
	state.Observe(ToolCall{Name: "list_dir", Args: map[string]any{"path": "."}}, "README.md\ncmd/\ninternal/\n")
	state.Observe(ToolCall{Name: "list_dir", Args: map[string]any{"path": "internal/harness"}}, "classifier.go\nplanner.go\npolicy.go\nstrictlocal.go\nthread.go\n")
	state.Observe(ToolCall{Name: "read_file", Args: map[string]any{"path": "internal/agent/progress.go"}}, "package agent\n")
	state.Observe(ToolCall{Name: "read_file", Args: map[string]any{"path": "README.md"}}, "# Forge\n")

	if state.QuickTourEnoughEvidence() {
		t.Fatal("expected implementation-grounded inspect to require a relevant harness source read before answering")
	}
	if msg := state.QuickTourNudgeMessage(); strings.Contains(msg, "README.md") {
		t.Fatalf("unexpected README detour in implementation-grounded nudge: %q", msg)
	}
}

func TestInspectRepositoryImplementationGroundedQuestionDoesNotRequireRootListingAfterRelevantReads(t *testing.T) {
	state := newInspectRepositoryEvidenceState("/repo", strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: repository
This is a read-only inspection turn.
USER REQUEST:
explain how the harness routes preview follow-ups in this repo`))

	state.Observe(ToolCall{Name: "search", Args: map[string]any{"pattern": "preview"}}, strings.Join([]string{
		"./internal/harness/strictlocal_test.go:65: preview_server_ensure",
		"./internal/harness/local_test.go:471: explain how the harness routes preview follow-ups in this repo",
	}, "\n"))
	state.Observe(ToolCall{Name: "read_file", Args: map[string]any{"path": "internal/harness/runner.go"}}, "package harness\n")
	state.Observe(ToolCall{Name: "read_file", Args: map[string]any{"path": "internal/harness/local.go"}}, "package harness\n")

	if !state.QuickTourEnoughEvidence() {
		t.Fatal("expected implementation-grounded inspect to become complete from relevant harness code even without a root listing")
	}
}

func TestInspectRepositoryImplementationGroundedQuestionKeepsNudgeOnAlignedSourceArea(t *testing.T) {
	state := newInspectRepositoryEvidenceState("/repo", strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: repository
This is a read-only inspection turn.
USER REQUEST:
explain how the harness routes preview follow-ups in this repo`))

	state.Observe(ToolCall{Name: "search", Args: map[string]any{"pattern": "preview"}}, strings.Join([]string{
		"./internal/harness/strictlocal_test.go:65: preview_server_ensure",
		"./internal/runtime/chat.go:516: previewRuntime := registerTools(reg, setup.WorkDir, setup.Config, approve)",
	}, "\n"))
	state.Observe(ToolCall{Name: "read_file", Args: map[string]any{"path": "internal/harness/strictlocal.go"}}, "package harness\n")

	msg := state.QuickTourNudgeMessage()
	if strings.Contains(msg, "internal/runtime/chat.go") {
		t.Fatalf("unexpected nudge drift into unrelated runtime file: %q", msg)
	}
	if !strings.Contains(msg, "internal/harness") {
		t.Fatalf("expected nudge to stay on the aligned harness area, got %q", msg)
	}
}

func TestStrictInspectRepositoryImplementationGroundedAnswerRejectsMissingFileReference(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "harness"), 0o755); err != nil {
		t.Fatalf("mkdir internal/harness: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "harness", "runner.go"), []byte("package harness\n"), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "harness", "strictlocal.go"), []byte("package harness\n"), 0o644); err != nil {
		t.Fatalf("write strictlocal.go: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			nil,
			nil,
			func(messages []llm.Message) error {
				if len(messages) == 0 {
					return fmt.Errorf("missing control history")
				}
				last := messages[len(messages)-1].Content
				lower := strings.ToLower(last)
				if !strings.Contains(lower, "do not cite file paths that do not exist") || !strings.Contains(last, "internal/harness/strict_local.go") {
					return fmt.Errorf("expected missing-file correction nudge, got %q", last)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/harness/runner.go\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/harness/strictlocal.go\"}}\n</tool_call>",
			"Preview follow-ups route through internal/harness/runner.go and internal/harness/strict_local.go.",
			"Preview follow-ups route through internal/harness/runner.go and internal/harness/strictlocal.go.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 8, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: repository
This is a read-only inspection turn.
USER REQUEST:
explain how the harness routes preview follow-ups in this repo`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 4 {
		t.Fatalf("expected strict inspect run to reject the missing file reference and continue, got %d calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "internal/harness/strictlocal.go") {
		t.Fatalf("final output missing corrected file reference: %q", output.String())
	}
	if strings.Contains(output.String(), "internal/harness/strict_local.go") {
		t.Fatalf("final output still contains missing file reference: %q", output.String())
	}
}

func TestStrictInspectRepositoryEarlyAnswerRetriesUntilQuickTourEvidenceComplete(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "forge"), 0o755); err != nil {
		t.Fatalf("mkdir cmd/forge: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Forge\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module forge\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "forge", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "agent.go"), []byte("package agent\n"), 0o644); err != nil {
		t.Fatalf("write agent.go: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			nil,
			nil,
			func(messages []llm.Message) error {
				if len(messages) == 0 {
					return fmt.Errorf("missing control history")
				}
				last := messages[len(messages)-1].Content
				lower := strings.ToLower(last)
				if !strings.Contains(lower, "repo-tour evidence is still incomplete") || !strings.Contains(lower, "representative implementation file") || !strings.Contains(last, "cmd") {
					return fmt.Errorf("expected repository retry nudge after early answer, got %q", last)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\"cmd\"}}\n</tool_call>",
			"This repo is a Go coding tool with docs at the root.",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"cmd/forge/main.go\"}}\n</tool_call>",
			"This repo is a Go terminal-first coding agent with docs at the root, a cmd entrypoint, and most implementation under internal/.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: repository
This is a read-only inspection turn.
USER REQUEST:
give me a quick repo tour and keep me updated as you go`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 6 {
		t.Fatalf("expected repository inspect run to reject the early answer and continue inspecting, got %d driver calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "This repo is a Go terminal-first coding agent with docs at the root, a cmd entrypoint, and most implementation under internal/.") {
		t.Fatalf("final output missing completion text: %q", output.String())
	}
}

func TestStrictInspectRepositoryRecognizesServiceSourceTreesForQuickTours(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "service"), 0o755); err != nil {
		t.Fatalf("mkdir service: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Fixture\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "service", "main.py"), []byte("def main():\n    return 1\n"), 0o644); err != nil {
		t.Fatalf("write service/main.py: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			nil,
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				lower := strings.ToLower(joined)
				if !strings.Contains(lower, "enough evidence") || !strings.Contains(lower, "repo tour") || !strings.Contains(lower, "answer now") {
					return fmt.Errorf("expected repository enough-evidence answer nudge after reading service/main.py, got %q", joined)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"service/main.py\"}}\n</tool_call>",
			"This repo is a small Python service with documentation at the root and the implementation under service/.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: repository
This is a read-only inspection turn.
USER REQUEST:
give me a quick repo tour and keep me updated as you go`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 4 {
		t.Fatalf("expected strict repository inspect run to accept service/main.py as representative source evidence, got %d driver calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "This repo is a small Python service with documentation at the root and the implementation under service/.") {
		t.Fatalf("final output missing completion text: %q", output.String())
	}
}

func TestStrictInspectSingleFileRequiresTargetReadBeforeAnswering(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("def greet(name):\n    return f\"Hello, {name}!\"\n"), 0o644); err != nil {
		t.Fatalf("write app.py: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Fixture\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			func(messages []llm.Message) error {
				if len(messages) == 0 {
					return fmt.Errorf("missing control history")
				}
				last := messages[len(messages)-1].Content
				lower := strings.ToLower(last)
				if !strings.Contains(lower, "single-file evidence is still incomplete") || !strings.Contains(last, "app.py") {
					return fmt.Errorf("expected single-file retry nudge before answering, got %q", last)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
			"app.py is a small greeting script.",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"app.py\"}}\n</tool_call>",
			"app.py defines greet(name) and prints greet(\"world\") when run directly.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 8, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: single-file
This is a read-only inspection turn.
USER REQUEST:
actually leave that alone and explain app.py`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 4 {
		t.Fatalf("expected single-file inspect run to force a read of app.py before answering, got %d calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "app.py defines greet(name) and prints greet(\"world\") when run directly.") {
		t.Fatalf("final output missing completion text: %q", output.String())
	}
}

func TestStrictInspectSingleFileEnoughEvidenceNudgesAnswerInsteadOfMoreTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("def greet(name):\n    return f\"Hello, {name}!\"\n"), 0o644); err != nil {
		t.Fatalf("write app.py: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatalf("mkdir web: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "web", "index.html"), []byte("<h1>preview</h1>\n"), 0o644); err != nil {
		t.Fatalf("write web/index.html: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				lower := strings.ToLower(joined)
				if !strings.Contains(lower, "enough evidence") || !strings.Contains(lower, "file walkthrough") || !strings.Contains(lower, "answer now") || !strings.Contains(joined, "app.py") {
					return fmt.Errorf("expected single-file enough-evidence nudge, got %q", joined)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"app.py\"}}\n</tool_call>",
			"app.py defines greet(name) and prints greet(\"world\") when run directly.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 8, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: single-file
This is a read-only inspection turn.
USER REQUEST:
actually leave that alone and explain app.py`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 3 {
		t.Fatalf("expected single-file inspect run to answer after reading app.py once, got %d calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "app.py defines greet(name) and prints greet(\"world\") when run directly.") {
		t.Fatalf("final output missing completion text: %q", output.String())
	}
}

func TestStrictInspectFocusedFilesEnoughEvidenceNudgesAnswerInsteadOfMoreTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "agent.go"), []byte("package agent\n\ntype Agent struct{}\n"), 0o644); err != nil {
		t.Fatalf("write agent.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "roles.go"), []byte("package agent\n\nvar Roles = map[string]string{\"dispatch\": \"Dispatch prompt\"}\n"), 0o644); err != nil {
		t.Fatalf("write roles.go: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			nil,
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				lower := strings.ToLower(joined)
				if !strings.Contains(lower, "enough evidence") || !strings.Contains(lower, "sampled") || !strings.Contains(lower, "answer now") {
					return fmt.Errorf("expected focused-files enough-evidence answer nudge, got %q", joined)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"glob\",\"args\":{\"pattern\":\"internal/agent/*.go\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/agent/agent.go\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/agent/roles.go\"}}\n</tool_call>",
			"I sampled internal/agent/agent.go and internal/agent/roles.go. agent.go defines the main runtime agent state, while roles.go defines role prompts and tool access.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewGlob(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 8, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: focused-files
This is a read-only inspection turn.
USER REQUEST:
give me a quick tour of the Go files under internal/agent and keep me updated as you go`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 4 {
		t.Fatalf("expected strict focused-files inspect run to answer once representative files were sampled, got %d driver calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "I sampled internal/agent/agent.go and internal/agent/roles.go.") {
		t.Fatalf("final output missing completion text: %q", output.String())
	}
}

func TestStrictInspectFocusedFilesDiscoveryNudgesReadsBeforeMoreDiscovery(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "agent.go"), []byte("package agent\n\ntype Agent struct{}\n"), 0o644); err != nil {
		t.Fatalf("write agent.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "agent_test.go"), []byte("package agent\n\nfunc TestAgent() {}\n"), 0o644); err != nil {
		t.Fatalf("write agent_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "roles.go"), []byte("package agent\n\nvar Roles = map[string]string{\"dispatch\": \"Dispatch prompt\"}\n"), 0o644); err != nil {
		t.Fatalf("write roles.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "parse.go"), []byte("package agent\n\nfunc ParseToolCalls() {}\n"), 0o644); err != nil {
		t.Fatalf("write parse.go: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			func(messages []llm.Message) error {
				if len(messages) == 0 {
					return fmt.Errorf("missing control history")
				}
				last := messages[len(messages)-1].Content
				lower := strings.ToLower(last)
				if !strings.Contains(lower, "focused-file evidence is still incomplete") || !strings.Contains(lower, "use read_file on 3 more representative matching file") {
					return fmt.Errorf("expected focused-files read nudge after discovery, got %q", last)
				}
				if !strings.Contains(last, "internal/agent/agent.go") || !strings.Contains(last, "internal/agent/roles.go") || !strings.Contains(last, "internal/agent/parse.go") {
					return fmt.Errorf("expected implementation-file targets in nudge, got %q", last)
				}
				if strings.Contains(last, "internal/agent/agent_test.go") {
					return fmt.Errorf("expected nudge to deprioritize test files, got %q", last)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\"internal/agent\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/agent/agent.go\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/agent/roles.go\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/agent/parse.go\"}}\n</tool_call>",
			"I sampled internal/agent/agent.go, internal/agent/roles.go, and internal/agent/parse.go. agent.go defines runtime state, roles.go defines role prompts, and parse.go normalizes tool-call output.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 8, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: focused-files
This is a read-only inspection turn.
USER REQUEST:
give me a quick tour of the Go files under internal/agent and keep me updated as you go`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 5 {
		t.Fatalf("expected focused-files inspect run to move from discovery into representative reads, got %d driver calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "I sampled internal/agent/agent.go, internal/agent/roles.go, and internal/agent/parse.go.") {
		t.Fatalf("final output missing completion text: %q", output.String())
	}
}

func TestStrictInspectFocusedFilesEarlyAnswerRetriesUntilEvidenceComplete(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "agent.go"), []byte("package agent\n\ntype Agent struct{}\n"), 0o644); err != nil {
		t.Fatalf("write agent.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "agent_test.go"), []byte("package agent\n\nfunc TestAgent() {}\n"), 0o644); err != nil {
		t.Fatalf("write agent_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "roles.go"), []byte("package agent\n\nvar Roles = map[string]string{\"dispatch\": \"Dispatch prompt\"}\n"), 0o644); err != nil {
		t.Fatalf("write roles.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "parse.go"), []byte("package agent\n\nfunc ParseToolCalls() {}\n"), 0o644); err != nil {
		t.Fatalf("write parse.go: %v", err)
	}

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			nil,
			nil,
			func(messages []llm.Message) error {
				if len(messages) == 0 {
					return fmt.Errorf("missing control history")
				}
				last := messages[len(messages)-1].Content
				lower := strings.ToLower(last)
				if !strings.Contains(lower, "focused-file evidence is still incomplete") || !strings.Contains(lower, "use read_file on 2 more representative matching file") {
					return fmt.Errorf("expected focused-files retry nudge after early answer, got %q", last)
				}
				if !strings.Contains(last, "internal/agent/roles.go") || !strings.Contains(last, "internal/agent/parse.go") {
					return fmt.Errorf("expected remaining implementation-file targets in retry nudge, got %q", last)
				}
				if strings.Contains(last, "internal/agent/agent_test.go") {
					return fmt.Errorf("expected retry nudge to deprioritize test files, got %q", last)
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\"internal/agent\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/agent/agent.go\"}}\n</tool_call>",
			"I sampled internal/agent/agent.go and can answer from here.",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/agent/roles.go\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/agent/parse.go\"}}\n</tool_call>",
			"I sampled internal/agent/agent.go, internal/agent/roles.go, and internal/agent/parse.go. agent.go defines runtime state, roles.go defines role prompts, and parse.go normalizes tool-call output.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 8, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: focused-files
This is a read-only inspection turn.
USER REQUEST:
give me a quick tour of the Go files under internal/agent and keep me updated as you go`)); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 6 {
		t.Fatalf("expected focused-files inspect run to reject the early answer and continue sampling, got %d driver calls", driver.callIdx)
	}
	if !strings.Contains(output.String(), "I sampled internal/agent/agent.go, internal/agent/roles.go, and internal/agent/parse.go.") {
		t.Fatalf("final output missing completion text: %q", output.String())
	}
}

func TestStrictInspectFinalAnswerAllowsLiteralToolMarkupMentions(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "agent.go"), []byte("package agent\n\ntype Agent struct{}\n"), 0o644); err != nil {
		t.Fatalf("write agent.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "roles.go"), []byte("package agent\n\nvar Roles = map[string]string{\"dispatch\": \"Dispatch prompt\"}\n"), 0o644); err != nil {
		t.Fatalf("write roles.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "parse.go"), []byte("package agent\n\nfunc ParseToolCalls() {}\n"), 0o644); err != nil {
		t.Fatalf("write parse.go: %v", err)
	}

	driver := &inspectingDriver{
		responses: []string{
			"<tool_call>\n{\"name\":\"glob\",\"args\":{\"pattern\":\"internal/agent/*.go\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/agent/agent.go\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/agent/roles.go\"}}\n</tool_call>",
			"<tool_call>\n{\"name\":\"read_file\",\"args\":{\"path\":\"internal/agent/parse.go\"}}\n</tool_call>",
			"I sampled `agent.go`, `roles.go`, and `parse.go`. `parse.go` handles XML-style wrappers like `<tool_call>`, `<function_calls>`, and `<tool_calls>`.",
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewGlob(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 5, renderer, nil, nil)
	agent.SetRole("strictlocal")
	agent.SetSystem(BuildStrictLocalSystemPrompt(dir, reg, nil))

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: focused-files
This is a read-only inspection turn.
USER REQUEST:
give me a quick tour of the Go files under internal/agent and keep me updated as you go`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(agent.LastResponse(), "`parse.go` handles XML-style wrappers like `<tool_call>`, `<function_calls>`, and `<tool_calls>`.") {
		t.Fatalf("final response missing literal tool-markup explanation: %q", agent.LastResponse())
	}
}
