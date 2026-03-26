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
	"forge/internal/skills"
)

// mockDriver returns predefined responses in sequence.
type mockDriver struct {
	responses []string
	callIdx   int
	callCount int
}

func (d *mockDriver) Name() string { return "mock" }

func (d *mockDriver) Stream(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	if d.callIdx >= len(d.responses) {
		out <- llm.Token{Text: "done"}
		return nil
	}
	resp := d.responses[d.callIdx]
	d.callIdx++
	out <- llm.Token{Text: resp}
	return nil
}

type inspectingDriver struct {
	checks    []func([]llm.Message) error
	responses []string
	callIdx   int
}

func (d *inspectingDriver) Name() string { return "inspecting" }

func (d *inspectingDriver) Stream(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	if d.callIdx < len(d.checks) && d.checks[d.callIdx] != nil {
		if err := d.checks[d.callIdx](messages); err != nil {
			return err
		}
	}
	resp := "done"
	if d.callIdx < len(d.responses) {
		resp = d.responses[d.callIdx]
	}
	d.callIdx++
	out <- llm.Token{Text: resp}
	return nil
}

func TestLooksLikeActionPreamble(t *testing.T) {
	cases := map[string]bool{
		"I’m going to inspect the code.":                  true,
		"I\u2019m going to inspect the code":              true, // smart quote
		"Next I’ll read the file.":                        true,
		"Let me check that.":                              true,
		"I’ll fix it.":                                    true,
		"First, I need to read the config.":               true,
		"Based on the error, we should fix the handler.":  true,
		"Looking at the code, there are two issues.":      true,
		"To accomplish this, I’ll start by reading main.": true,
		"Would you like me to continue?":                  true,
		"Here is the fix.":                                false,
		"The issue is in chatmodel.go.":                   false,
		"Done. All tests pass.":                           false,
	}
	for input, want := range cases {
		if got := looksLikeActionPreamble(input); got != want {
			t.Fatalf("looksLikeActionPreamble(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestAgentRunNoTools(t *testing.T) {
	driver := &mockDriver{responses: []string{"Hello! I can help with that."}}
	reg := tools.NewRegistry()
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), "/tmp", 10, renderer, nil, nil)
	err := agent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Hello") {
		t.Errorf("output = %q", output.String())
	}
}

func TestAgentRunSetsLastResponseOnSuccess(t *testing.T) {
	driver := &mockDriver{responses: []string{"Hello! I can help with that."}}
	reg := tools.NewRegistry()
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), "/tmp", 10, renderer, nil, nil)
	if err := agent.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if got := agent.LastResponse(); got != "Hello! I can help with that." {
		t.Fatalf("LastResponse() = %q", got)
	}
}

func TestAgentRunWithToolCall(t *testing.T) {
	dir := t.TempDir()
	driver := &mockDriver{responses: []string{
		"Let me read the file.\n\n<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"I see the directory listing. All done.",
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	err := agent.Run(context.Background(), "list files")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunDoesNotPersistVisibleProseFromToolTurns(t *testing.T) {
	dir := t.TempDir()
	driver := &mockDriver{responses: []string{
		"Reviewing the repo structure now.\n\n<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"Done.",
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	if err := agent.Run(context.Background(), "list files"); err != nil {
		t.Fatal(err)
	}

	for _, msg := range agent.history {
		if msg.Role == llm.RoleAssistant && strings.Contains(msg.Content, "Reviewing the repo structure now.") {
			t.Fatalf("tool-turn prose leaked into assistant history: %#v", agent.history)
		}
	}
}

func TestAgentRunDoesNotPersistHarnessInspectWrapperIntoFollowUpTurns(t *testing.T) {
	dir := t.TempDir()
	driver := &inspectingDriver{
		responses: []string{
			"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
			"Directory contains cmd and internal.",
			"cleanup_folder.py would be a good start.",
		},
		checks: []func([]llm.Message) error{
			nil,
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				if strings.Contains(joined, "HARNESS MODE: inspect") {
					return fmt.Errorf("inspect wrapper leaked into follow-up turn: %s", joined)
				}
				if strings.Contains(joined, "USER REQUEST:") {
					return fmt.Errorf("inspect user-request wrapper leaked into follow-up turn: %s", joined)
				}
				if !strings.Contains(joined, "describe this directory") {
					return fmt.Errorf("expected raw inspect request to remain in history, got: %s", joined)
				}
				if !strings.Contains(joined, "Directory contains cmd and internal.") {
					return fmt.Errorf("expected prior inspect answer to remain in history, got: %s", joined)
				}
				return nil
			},
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)

	if err := agent.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: directory
This is a read-only inspection turn.
USER REQUEST:
describe this directory`)); err != nil {
		t.Fatal(err)
	}
	if err := agent.Run(context.Background(), "write me a py script that cleans up the folder"); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "cleanup_folder.py would be a good start.") {
		t.Fatalf("expected follow-up answer to render, got %q", got)
	}
}

func TestAgentRunWithFunctionCallsDoesNotLeakRawWrapper(t *testing.T) {
	dir := t.TempDir()
	driver := &mockDriver{responses: []string{
		"Let me inspect that.\n\n<function_calls>\n[{\"name\": \"list_dir\", \"args\": {}}]\n</function_calls>",
		"Done.",
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	if err := agent.Run(context.Background(), "list files"); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); strings.Contains(got, "<function_calls>") || strings.Contains(got, "</function_calls>") {
		t.Fatalf("raw function_calls wrapper leaked to renderer output: %q", got)
	}
}

func TestAgentRunWithInvokeDoesNotLeakRawWrapper(t *testing.T) {
	dir := t.TempDir()
	driver := &mockDriver{responses: []string{
		"Let me inspect that.\n\n<invoke>\n{\"name\": \"list_dir\", \"args\": {}}\n</invoke>",
		"Done.",
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	if err := agent.Run(context.Background(), "list files"); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); strings.Contains(got, "<invoke>") || strings.Contains(got, "</invoke>") {
		t.Fatalf("raw invoke wrapper leaked to renderer output: %q", got)
	}
}

func TestAgentToolHelpRevealsHiddenToolsForNextTurn(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "read_file",
		Description: "Read a file",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "ok", nil
		},
	})
	reg.Register(tools.NewToolHelp(reg))
	hiddenCalled := false
	reg.Register(tools.Tool{
		Name:             "web_search",
		Description:      "Search the web",
		PromptVisibility: tools.PromptHidden,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			hiddenCalled = true
			return "search results", nil
		},
	})

	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			func(messages []llm.Message) error {
				if len(messages) == 0 || !strings.Contains(messages[0].Content, "tool_help") {
					return fmt.Errorf("missing tool_help in initial prompt")
				}
				if strings.Contains(messages[0].Content, "web_search") {
					return fmt.Errorf("hidden tool leaked into initial prompt")
				}
				return nil
			},
			func(messages []llm.Message) error {
				if len(messages) == 0 || !strings.Contains(messages[0].Content, "web_search") {
					return fmt.Errorf("hidden tool not disclosed after tool_help")
				}
				return nil
			},
		},
		responses: []string{
			"<tool_call>\n{\"name\": \"tool_help\", \"args\": {\"query\": \"search the web\"}}\n</tool_call>",
			"<tool_call>\n{\"name\": \"web_search\", \"args\": {\"query\": \"forge repo\"}}\n</tool_call>",
			"done",
		},
	}

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	if err := agent.Run(context.Background(), "look something up"); err != nil {
		t.Fatal(err)
	}
	if !hiddenCalled {
		t.Fatal("expected hidden tool to be called after disclosure")
	}
}

func TestAgentMaxTurns(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(t.TempDir(), nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 3, renderer, nil, nil)
	err := agent.Run(context.Background(), "loop forever")
	if err != nil && !strings.Contains(err.Error(), "max turns") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompressHistory(t *testing.T) {
	a := &Agent{maxTurns: 50}

	for i := 0; i < 20; i++ {
		a.history = append(a.history, llm.Message{
			Role:    llm.RoleAssistant,
			Content: fmt.Sprintf("<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"file%d.go\"}}\n</tool_call>", i),
		})
		a.history = append(a.history, llm.Message{
			Role:    llm.RoleUser,
			Content: "Tool results:\n\n[read_file] " + strings.Repeat("x", 5000),
		})
	}

	a.compressHistory(50000)

	totalLen := 0
	for _, m := range a.history {
		totalLen += len(m.Content)
	}
	if totalLen > 55000 {
		t.Errorf("compressed history too large: %d chars", totalLen)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens("hello world"); got < 2 || got > 4 {
		t.Errorf("estimateTokens('hello world') = %d", got)
	}
}

func TestAgentRunAllowsActivatedSkill(t *testing.T) {
	driver := &mockDriver{responses: []string{"planned"}}
	reg := tools.NewRegistry()
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	s := skills.Skill{Name: "brainstorming", Description: "Planning", Body: "Plan first."}

	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, []skills.Skill{s}, nil)
	a.InjectSkill(s)
	err := a.Run(context.Background(), "please plan the architecture first")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunRetriesActionPreambleBeforeToolCall(t *testing.T) {
	dir := t.TempDir()
	driver := &mockDriver{responses: []string{
		"I'm going to inspect the file first.",
		"I noticed we need to inspect the file first.",
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>",
		"Done.",
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	if err := agent.Run(context.Background(), "inspect the directory"); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 4 {
		t.Fatalf("expected 4 driver calls, got %d", driver.callIdx)
	}
	if got := strings.Join(func() []string {
		out := make([]string, 0, len(agent.history))
		for _, m := range agent.history {
			out = append(out, m.Content)
		}
		return out
	}(), "\n"); strings.Contains(got, "I'm going to inspect the file first.") {
		t.Fatalf("history should not keep raw action preambles: %q", got)
	}
}

func TestAgentRunDoesNotRetryShortFinalAnswer(t *testing.T) {
	driver := &mockDriver{responses: []string{"Hello! I'm forge, your coding agent. How can I help you with your project today?"}}
	reg := tools.NewRegistry()
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)

	agent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	if err := agent.Run(context.Background(), "testing sonnet, hello"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 1 {
		t.Fatalf("expected 1 driver call for short final answer, got %d", driver.callCount)
	}
	if got := output.String(); strings.Count(got, "How can I help you with your project today?") != 1 {
		t.Fatalf("expected one rendered greeting, got %q", got)
	}
}

func TestCompactAssistantHistory(t *testing.T) {
	got := compactAssistantHistory("I'm going to inspect the file first.")
	if got != "I'm going to inspect the file first." {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "<tool_call>") {
		t.Fatalf("got %q", got)
	}
}

func TestCompactAssistantHistoryEmptyWhenNoVisibleText(t *testing.T) {
	if got := compactAssistantHistory(""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestCompactToolResults(t *testing.T) {
	got := compactToolResults([]string{
		"[read_file] line1\nline2\nline3",
		strings.Repeat("x", 13000),
	})
	if !strings.Contains(got, "Tool results:") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "\n  line2") {
		t.Fatalf("expected multiline result to be preserved: %q", got)
	}
	if !strings.Contains(got, "truncated in history") {
		t.Fatalf("expected long result to be clipped with an explicit marker: %q", got)
	}
	if len(got) > 13000 {
		t.Fatalf("compact results too large: %d", len(got))
	}
}

func TestScoutTaskIsRepoReviewRequiresRepositoryScope(t *testing.T) {
	fileSummaryTask := "TASK: Inspect and summarize the file @util-rancid/influx/update-influx-file.py for the user. OUTCOME: Provide a concise explanation of what the script does, its key functions/flow, inputs/outputs, dependencies, and any noteworthy implementation details."
	if scoutTaskIsRepoReview(fileSummaryTask) {
		t.Fatalf("targeted file-summary task should not be treated as a repo review: %q", fileSummaryTask)
	}

	loggedDispatchTask := "TASK: Inspect the repository and gather comprehensive information about the file named inv_stats_to_influx_elastic.py (or similarly named path if prefixed with @ in the user message). Determine its exact location, summarize its purpose, key functions/classes, inputs/outputs, dependencies, configuration, how it is invoked, and any notable risks or TODOs. OUTCOME: Return a detailed read-only report based on the file contents and nearby context."
	if scoutTaskIsRepoReview(loggedDispatchTask) {
		t.Fatalf("single-file task with repository preamble should not be treated as a repo review: %q", loggedDispatchTask)
	}

	repoReviewTask := "TASK: Gather evidence only for a repo review. OUTCOME: Evidence-backed findings only. Gather repository purpose, structure, tech stack, key modules, dependencies, and test/build health."
	if !scoutTaskIsRepoReview(repoReviewTask) {
		t.Fatalf("explicit repo-review task should be detected: %q", repoReviewTask)
	}
}

func TestScoutRunAllowsTargetedFileSummaryThatMentionsDependencies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "util-rancid", "influx")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "update-influx-file.py"), []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"util-rancid/influx/update-influx-file.py\"}}\n</tool_call>",
		`{"status":"complete","message":"Summary ready.","artifact_kind":"evidence","artifact":"Reads the script and summarizes it.","next_role":"","next_task":""}`,
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), dir, 2, renderer, nil, nil)
	a.SetRole("scout")
	a.isSubAgent = true

	task := "TASK: Inspect and summarize the file @util-rancid/influx/update-influx-file.py for the user. OUTCOME: Provide a concise explanation of what the script does, its key functions/flow, inputs/outputs, dependencies, and any noteworthy implementation details. MUST NOT: Modify files or make code changes."
	if err := a.Run(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 2 {
		t.Fatalf("expected scout to finish in two turns, got %d calls", driver.callCount)
	}
	for _, m := range a.history {
		if strings.Contains(m.Content, "Repo-review evidence is still incomplete") {
			t.Fatalf("targeted file-summary task should not receive repo-review nudges: %#v", a.history)
		}
	}
}

func TestScoutRunSingleFileRepositoryPreambleReadsTargetBeforeStopping(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "inv_stats_to_influx_elastic.py"), []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"glob\", \"args\": {\"pattern\": \"**/inv_stats_to_influx_elastic.py\"}}\n</tool_call>",
		"I found the file name `inv_stats_to_influx_elastic.py`, but I don't yet have the actual file path or contents to report on.",
		"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"inv_stats_to_influx_elastic.py\"}}\n</tool_call>",
		`{"status":"complete","message":"Summary ready.","artifact_kind":"evidence","artifact":"Reads the file after locating it.","next_role":"","next_task":""}`,
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.NewGlob(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), dir, 6, renderer, nil, nil)
	a.SetRole("scout")
	a.isSubAgent = true

	task := "TASK: Inspect the repository and gather comprehensive information about the file named inv_stats_to_influx_elastic.py (or similarly named path if prefixed with @ in the user message). Determine its exact location, summarize its purpose, key functions/classes, inputs/outputs, dependencies, configuration, how it is invoked, and any notable risks or TODOs. OUTCOME: Return a detailed read-only report based on the file contents and nearby context. MUST NOT: Do not edit files; do not make assumptions without checking the code; do not omit file path and invocation/context if discoverable."
	if err := a.Run(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 4 {
		t.Fatalf("expected scout to keep going until it read the target file, got %d calls", driver.callCount)
	}
	for _, m := range a.history {
		if strings.Contains(m.Content, "Repo-review evidence is still incomplete") {
			t.Fatalf("single-file task should not receive repo-review nudges: %#v", a.history)
		}
	}
}

func TestRewriteDispatchScoutTaskAddsSingleFileScopeMetadata(t *testing.T) {
	task := "TASK: Inspect the repository and gather comprehensive information about the file named inv_stats_to_influx_elastic.py (or similarly named path if prefixed with @ in the user message). Determine its exact location, summarize its purpose, key functions/classes, inputs/outputs, dependencies, configuration, how it is invoked, and any notable risks or TODOs. OUTCOME: Return a detailed read-only report based on the file contents and nearby context."

	rewritten := rewriteDispatchScoutTask(task)

	if !strings.Contains(rewritten, "\nSCOPE: single-file") {
		t.Fatalf("expected single-file scope metadata, got %q", rewritten)
	}
	if !strings.Contains(rewritten, "\nTARGET: inv_stats_to_influx_elastic.py") {
		t.Fatalf("expected single-file target metadata, got %q", rewritten)
	}
}

func TestCompressHistoryCompactsOldSkillAndConversationMessages(t *testing.T) {
	a := &Agent{maxTurns: 20}
	a.history = append(a.history,
		llm.Message{Role: llm.RoleUser, Content: "[Skill: brainstorming]\n\n" + strings.Repeat("plan first ", 60)},
		llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("assistant text ", 40)},
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("user text ", 40)},
		llm.Message{Role: llm.RoleAssistant, Content: "recent assistant"},
		llm.Message{Role: llm.RoleUser, Content: "recent user"},
	)

	a.compressHistory(10)

	if got := a.history[0].Content; got != "[Skill: brainstorming]" {
		t.Fatalf("unexpected compacted skill content: %q", got)
	}
	if len(a.history[1].Content) > 240 {
		t.Fatalf("assistant message not compacted: %d", len(a.history[1].Content))
	}
	if got := a.history[2].Content; got != strings.Repeat("user text ", 40) {
		t.Fatalf("third-most-recent message should be preserved, got %q", got)
	}
	if got := a.history[3].Content; got != "recent assistant" {
		t.Fatalf("recent assistant should be preserved, got %q", got)
	}
	if got := a.history[4].Content; got != "recent user" {
		t.Fatalf("recent user should be preserved, got %q", got)
	}
}

func TestEnforceHistoryBudgetSkipsCompactionWhenUnderBudget(t *testing.T) {
	a := &Agent{
		workDir: t.TempDir(),
		tools:   tools.NewRegistry(),
		history: []llm.Message{
			{Role: llm.RoleUser, Content: "[Skill: brainstorming]\n\n" + strings.Repeat("plan ", 20)},
			{Role: llm.RoleAssistant, Content: "short reply"},
		},
	}

	before := []string{a.history[0].Content, a.history[1].Content}
	a.enforceHistoryBudget(1000)

	if a.history[0].Content != before[0] || a.history[1].Content != before[1] {
		t.Fatalf("history should not change under budget: %#v", a.history)
	}
}

func TestSubAgentSkipsNudgeOnShortResponse(t *testing.T) {
	driver := &mockDriver{responses: []string{"FINDINGS:\n- found 3 files"}}
	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(t.TempDir(), nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.isSubAgent = true

	if err := a.Run(context.Background(), "find files"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 1 {
		t.Errorf("expected 1 LLM call, got %d (sub-agent was nudged)", driver.callCount)
	}
}

func TestLastFullResponsePreserved(t *testing.T) {
	longResponse := strings.Repeat("x", 500)
	driver := &mockDriver{responses: []string{longResponse}}
	reg := tools.NewRegistry()

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.isSubAgent = true

	if err := a.Run(context.Background(), "do something"); err != nil {
		t.Fatal(err)
	}
	if a.lastFullResponse != longResponse {
		t.Errorf("lastFullResponse length = %d, want %d", len(a.lastFullResponse), len(longResponse))
	}
}

func TestEnforceHistoryBudgetCompactsLargestOldMessagesFirst(t *testing.T) {
	a := &Agent{
		workDir: t.TempDir(),
		tools:   tools.NewRegistry(),
		history: []llm.Message{
			{Role: llm.RoleUser, Content: "[Skill: brainstorming]\n\n" + strings.Repeat("plan ", 100)},
			{Role: llm.RoleUser, Content: "Tool results:\n- " + strings.Repeat("x ", 150)},
			{Role: llm.RoleAssistant, Content: strings.Repeat("assistant ", 80)},
			{Role: llm.RoleUser, Content: "recent 1"},
			{Role: llm.RoleAssistant, Content: "recent 2"},
			{Role: llm.RoleUser, Content: "recent 3"},
		},
	}

	before := a.estimatedRequestTokens()
	a.enforceHistoryBudget(before / 2)

	if got := a.history[0].Content; got != "[Skill: brainstorming]" {
		t.Fatalf("expected skill content compacted, got %q", got)
	}
	if got := a.history[1].Content; got == "" {
		t.Fatalf("tool result history should remain present")
	}
	if got := a.history[3].Content; got != "recent 1" {
		t.Fatalf("recent message should be preserved, got %q", got)
	}
	if after := a.estimatedRequestTokens(); after >= before {
		t.Fatalf("expected estimated tokens to decrease: before=%d after=%d", before, after)
	}
}

func TestDispatchProseFilteredOnToolCallTurns(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"Let me delegate to scout.\n\n<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"find it\"}}\n</tool_call>",
		"Here are the results from scout.",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "scout found stuff", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "find something"); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if strings.Contains(got, "Let me delegate") {
		t.Errorf("dispatch prose before tool call leaked: %q", got)
	}
	if strings.Contains(got, "Here are the results") {
		t.Errorf("dispatch should not narrate delegated results: %q", got)
	}
	if !strings.Contains(got, "scout found stuff") {
		t.Errorf("delegate tool result missing from output: %q", got)
	}
}

func TestDispatchProseAfterToolCallInSameResponseIsFiltered(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"find it\"}}\n</tool_call>\nHere’s what stood out on a quick review.",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "scout found stuff", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "find something"); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if strings.Contains(got, "Here’s what stood out") || strings.Contains(got, "Here's what stood out") {
		t.Fatalf("dispatch prose after tool call leaked: %q", got)
	}
	if !strings.Contains(got, "scout found stuff") {
		t.Fatalf("delegate tool result missing from output: %q", got)
	}
}

func TestSubAgentProseAfterToolCallInSameResponseIsFiltered(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>\nPlease paste the tool outputs for the repo root.",
		"FINDINGS:\n- found files",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(t.TempDir(), nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.isSubAgent = true

	if err := a.Run(context.Background(), "inspect repo"); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if strings.Contains(got, "Please paste the tool outputs") {
		t.Fatalf("sub-agent prose after tool call leaked: %q", got)
	}
	if !strings.Contains(got, "found files") {
		t.Fatalf("final sub-agent findings missing: %q", got)
	}
}

func TestScoutExecutesToolCallsAndSuppressesVisibleProse(t *testing.T) {
	dir := t.TempDir()
	driver := &inspectingDriver{
		responses: []string{
			"<tool_call>\n{\"name\": \"list_dir\", \"args\": {}}\n</tool_call>\nPlease provide the pending tool results for the repo root.",
			"FINDINGS:\n- found files",
		},
		checks: []func([]llm.Message) error{
			nil,
			func(messages []llm.Message) error {
				var sawToolResults bool
				var sawNudge bool
				for _, msg := range messages {
					if strings.Contains(msg.Content, "[list_dir]") {
						sawToolResults = true
					}
					if strings.Contains(msg.Content, scoutNudgeMessage()) {
						sawNudge = true
					}
				}
				if !sawToolResults {
					return fmt.Errorf("second turn missing executed tool results")
				}
				if !sawNudge {
					return fmt.Errorf("second turn missing scout mixed-prose nudge")
				}
				return nil
			},
		},
	}
	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	a.SetRole("scout")
	a.isSubAgent = true

	if err := a.Run(context.Background(), "inspect repo"); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 2 {
		t.Fatalf("expected scout mixed-prose flow to keep executed tool results, got %d driver calls", driver.callIdx)
	}
	if got := output.String(); strings.Contains(got, "Please provide the pending tool results") {
		t.Fatalf("scout prose leak should be suppressed, got %q", got)
	}
}

func TestHarnessInspectTurnFirstWorkingTurnUsesSingleToolCall(t *testing.T) {
	driver := &inspectingDriver{
		responses: []string{
			"<tool_call>\n{\"name\": \"glob\", \"args\": {\"pattern\": \"**/*.py\", \"path\": \".\"}}\n</tool_call>\n<tool_call>\n{\"name\": \"git_status\", \"args\": {}}\n</tool_call>",
			"Focused Python review complete.",
		},
		checks: []func([]llm.Message) error{
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				if !strings.Contains(joined, "[glob]") {
					return fmt.Errorf("second turn missing executed first tool result")
				}
				if !strings.Contains(joined, "exactly one tool call per working turn") {
					return fmt.Errorf("second turn missing inspect single-tool-call nudge")
				}
				return nil
			},
		},
	}

	reg := tools.NewRegistry()
	var executed []string
	reg.Register(tools.Tool{
		Name:        "glob",
		Description: "Glob",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			executed = append(executed, "glob")
			return "alpha.py\nbeta.py", nil
		},
	})
	reg.Register(tools.Tool{
		Name:        "git_status",
		Description: "Git status",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			executed = append(executed, "git_status")
			return "?? alpha.py", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)

	err := a.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: focused-files
This is a read-only inspection turn.
USER REQUEST:
check the py files`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(executed, ","); got != "glob" {
		t.Fatalf("expected only first inspect tool call to execute, got %q", got)
	}
}

func TestHarnessInspectTurnSerializesLaterToolCallsAndSuppressesVisibleProse(t *testing.T) {
	driver := &inspectingDriver{
		responses: []string{
			"<tool_call>\n{\"name\": \"glob\", \"args\": {\"pattern\": \"**/*.py\", \"path\": \".\"}}\n</tool_call>",
			"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"alpha.py\"}}\n</tool_call>\n<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"beta.py\"}}\n</tool_call>\nI inspected enough to answer now.",
			"Alpha and beta look like ad hoc scripts with no shared project structure.",
		},
		checks: []func([]llm.Message) error{
			nil,
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				if !strings.Contains(joined, "[read_file] alpha.py") {
					return fmt.Errorf("third turn missing first read_file result")
				}
				if !strings.Contains(joined, "must not mix visible prose with tool calls") {
					return fmt.Errorf("third turn missing inspect mixed-prose nudge")
				}
				if !strings.Contains(joined, "exactly one tool call per working turn") {
					return fmt.Errorf("third turn missing inspect single-tool-call nudge")
				}
				return nil
			},
		},
	}

	reg := tools.NewRegistry()
	var executed []string
	reg.Register(tools.Tool{
		Name:        "glob",
		Description: "Glob",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			executed = append(executed, "glob")
			return "alpha.py\nbeta.py", nil
		},
	})
	reg.Register(tools.Tool{
		Name:        "read_file",
		Description: "Read file",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			executed = append(executed, "read_file:"+path)
			return path, nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)

	err := a.Run(context.Background(), strings.TrimSpace(`HARNESS MODE: inspect
INSPECT SCOPE: focused-files
This is a read-only inspection turn.
USER REQUEST:
check the py files`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(executed, ","); got != "glob,read_file:alpha.py" {
		t.Fatalf("expected inspect turn to serialize tool calls, got %q", got)
	}
	if got := output.String(); strings.Contains(got, "I inspected enough to answer now.") {
		t.Fatalf("inspect prose leak should be suppressed, got %q", got)
	}
}

func TestScoutExecutesBareJSONToolCallPrefixAndSuppressesVisibleProse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := &inspectingDriver{
		responses: []string{
			"{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\",\"start_line\":1,\"end_line\":260}}I need a bit more repository evidence before I can give a reliable, citation-backed overview and tidy-up assessment.",
			"FINDINGS:\n- README inspected successfully",
		},
		checks: []func([]llm.Message) error{
			nil,
			func(messages []llm.Message) error {
				var sawToolResults bool
				var sawNudge bool
				for _, msg := range messages {
					if strings.Contains(msg.Content, "[read_file]") && strings.Contains(msg.Content, "# Repo") {
						sawToolResults = true
					}
					if strings.Contains(msg.Content, scoutNudgeMessage()) {
						sawNudge = true
					}
				}
				if !sawToolResults {
					return fmt.Errorf("second turn missing executed read_file results")
				}
				if !sawNudge {
					return fmt.Errorf("second turn missing scout mixed-prose nudge")
				}
				return nil
			},
		},
	}
	reg := tools.NewRegistry()
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	a.SetRole("scout")
	a.isSubAgent = true

	if err := a.Run(context.Background(), "TASK: Inspect the repository and produce an evidence-backed overview. OUTCOME: Findings only. MUST NOT: Do not modify files."); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 2 {
		t.Fatalf("expected loose JSON tool call to execute and continue, got %d driver calls", driver.callIdx)
	}
	if got := output.String(); strings.Contains(got, "I need a bit more repository evidence") {
		t.Fatalf("scout prose leak should be suppressed, got %q", got)
	}
	if got := output.String(); !strings.Contains(got, "README inspected successfully") {
		t.Fatalf("final scout findings missing: %q", got)
	}
}

func TestSpawnSubAgentScoutRecoversFromMalformedToolMarkup(t *testing.T) {
	driver := &inspectingDriver{
		responses: []string{
			"<tool_call>{\"name\":\"search\",\"args\":{\"pattern\":\"Rancid f5 objstor verify script missing\",\"path\":\".\",\"glob\":\"**/*\"}}<tool_call>{\"name\":\"search\",\"args\":{\"pattern\":\"objstor verify\",\"path\":\".\",\"glob\":\"**/*\"}}",
			"<tool_call>\n{\"name\": \"search\", \"args\": {\"pattern\": \"Rancid f5 objstor verify script missing\", \"path\": \".\", \"glob\": \"**/*\"}}\n</tool_call>",
			`{"status":"complete","message":"Found the alert source.","artifact_kind":"evidence","artifact":"/repo/util-rancid/update_cerner_daily.sh:753 emits the alert","next_role":"","next_task":""}`,
		},
		checks: []func([]llm.Message) error{
			nil,
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				if !strings.Contains(joined, scoutMalformedToolMarkupNudgeMessage(1)) {
					return fmt.Errorf("second turn missing malformed-tool-markup nudge")
				}
				return nil
			},
			nil,
		},
	}

	reg := tools.NewRegistry()
	searchCalls := 0
	reg.Register(tools.Tool{
		Name:        "search",
		Description: "Search",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			searchCalls++
			return "/repo/util-rancid/update_cerner_daily.sh:753: run_or_warn \"f5 objstor verify missing-script alert email\"", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	parent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)

	result, err := parent.SpawnSubAgent(context.Background(), "scout", "TASK: Trace the origin of the email subject and identify why it would be sent. OUTCOME: Evidence-backed findings with file path and triggering condition. MUST NOT: Do not speculate.", MultiAgentConfig{
		BaseTools: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if searchCalls != 1 {
		t.Fatalf("expected one recovered scout search call, got %d", searchCalls)
	}
	if outcome := parseDelegateOutcome(result); !outcome.Structured || outcome.Message != "Found the alert source." {
		t.Fatalf("expected typed scout findings after malformed markup recovery, got %q", result)
	}
}

func TestSpawnSubAgentScoutRecoversFromBareJSONToolCallPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := &inspectingDriver{
		responses: []string{
			"{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\",\"start_line\":1,\"end_line\":260}}I need a bit more repository evidence before I can give a reliable, citation-backed overview and tidy-up assessment.",
			"Repository inspection shows this is an automation repo with mixed Bash, PowerShell, and Python workflows.",
			`{"status":"complete","message":"Repository inspection shows this is an automation repo with mixed Bash, PowerShell, and Python workflows.","artifact_kind":"evidence","artifact":"README.md confirms a multi-host automation repo.","next_role":"","next_task":""}`,
		},
	}

	reg := tools.NewRegistry()
	readCalls := 0
	readTool := tools.NewReadFile(dir)
	readTool.Execute = func(ctx context.Context, args map[string]any) (string, error) {
		readCalls++
		return "1 | # Repo", nil
	}
	reg.Register(readTool)

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	parent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)

	result, err := parent.SpawnSubAgent(context.Background(), "scout", "TASK: Inspect the repository and produce an evidence-backed overview. OUTCOME: Findings only. MUST NOT: Do not modify files.", MultiAgentConfig{
		BaseTools: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if readCalls != 1 {
		t.Fatalf("expected one recovered read_file call, got %d", readCalls)
	}
	if strings.Contains(result, "not able to inspect the key files") {
		t.Fatalf("expected scout to avoid blocked no-evidence fallback, got %q", result)
	}
	if outcome := parseDelegateOutcome(result); !outcome.Structured || outcome.Message != "Repository inspection shows this is an automation repo with mixed Bash, PowerShell, and Python workflows." {
		t.Fatalf("expected typed scout findings after loose JSON recovery, got %q", result)
	}
}

func TestSpawnSubAgentScoutFirstTurnUsesSingleToolCall(t *testing.T) {
	driver := &inspectingDriver{
		responses: []string{
			"<tool_call>{\"name\":\"search\",\"args\":{\"pattern\":\"first\",\"path\":\".\",\"glob\":\"**/*\"}}</tool_call><tool_call>{\"name\":\"search\",\"args\":{\"pattern\":\"second\",\"path\":\".\",\"glob\":\"**/*\"}}</tool_call>",
			`{"status":"complete","message":"Found the alert source.","artifact_kind":"evidence","artifact":"/repo/result","next_role":"","next_task":""}`,
		},
		checks: []func([]llm.Message) error{
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				if !strings.Contains(joined, scoutFirstTurnToolCallNudgeMessage()) {
					return fmt.Errorf("second turn missing first-turn single-tool-call nudge")
				}
				return nil
			},
		},
	}

	reg := tools.NewRegistry()
	var patterns []string
	reg.Register(tools.Tool{
		Name:        "search",
		Description: "Search",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			pattern, _ := args["pattern"].(string)
			patterns = append(patterns, pattern)
			return "/repo/result", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	parent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)

	result, err := parent.SpawnSubAgent(context.Background(), "scout", "TASK: Search the repository for the alert source. OUTCOME: Evidence-backed findings only. MUST NOT: Do not speculate.", MultiAgentConfig{
		BaseTools: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != 1 || patterns[0] != "first" {
		t.Fatalf("expected only first scout tool call to execute, got %v", patterns)
	}
	if outcome := parseDelegateOutcome(result); !outcome.Structured || outcome.Message != "Found the alert source." {
		t.Fatalf("expected typed scout findings after single-tool-call enforcement, got %q", result)
	}
}

func TestSpawnSubAgentScoutRetriesAfterEmptyFinalOutput(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"search\", \"args\": {\"pattern\": \"Rancid f5 objstor verify script missing\", \"path\": \".\", \"glob\": \"**/*\"}}\n</tool_call>",
		"",
		`{"status":"complete","message":"Found the alert source.","artifact_kind":"evidence","artifact":"/repo/util-rancid/update_cerner_daily.sh:753 emits the alert","next_role":"","next_task":""}`,
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "search",
		Description: "Search",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "/repo/util-rancid/update_cerner_daily.sh:753: run_or_warn \"f5 objstor verify missing-script alert email\"", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	parent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)

	result, err := parent.SpawnSubAgent(context.Background(), "scout", "TASK: Trace the alert source and return evidence-backed findings only. OUTCOME: file path plus trigger. MUST NOT: Do not speculate.", MultiAgentConfig{
		BaseTools: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 3 {
		t.Fatalf("expected empty final output to trigger a retry, got %d driver calls", driver.callIdx)
	}
	if strings.Contains(result, "AGENT ERROR") {
		t.Fatalf("expected scout to recover from empty final output, got %q", result)
	}
	if outcome := parseDelegateOutcome(result); !outcome.Structured || outcome.Message != "Found the alert source." {
		t.Fatalf("expected typed scout findings after empty output retry, got %q", result)
	}
}

func TestSpawnSubAgentScoutRetriesAfterPlainFinalOutput(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"search\", \"args\": {\"pattern\": \"Rancid f5 objstor verify script missing\", \"path\": \".\", \"glob\": \"**/*\"}}\n</tool_call>",
		"Found the alert source in util-rancid/update_cerner_daily.sh:753.",
		`{"status":"complete","message":"Found the alert source in util-rancid/update_cerner_daily.sh:753.","artifact_kind":"evidence","artifact":"/repo/util-rancid/update_cerner_daily.sh:753 emits the alert","next_role":"","next_task":""}`,
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "search",
		Description: "Search",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "/repo/util-rancid/update_cerner_daily.sh:753: run_or_warn \"f5 objstor verify missing-script alert email\"", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	parent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)

	result, err := parent.SpawnSubAgent(context.Background(), "scout", "TASK: Trace the alert source and return evidence-backed findings only. OUTCOME: file path plus trigger. MUST NOT: Do not speculate.", MultiAgentConfig{
		BaseTools: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 3 {
		t.Fatalf("expected plain scout final output to trigger a retry, got %d driver calls", driver.callIdx)
	}
	if strings.Contains(result, "AGENT ERROR") {
		t.Fatalf("expected scout to recover from plain final output, got %q", result)
	}
	if outcome := parseDelegateOutcome(result); !outcome.Structured || outcome.Message != "Found the alert source in util-rancid/update_cerner_daily.sh:753." {
		t.Fatalf("expected typed scout findings after plain output retry, got %q", result)
	}
	if got := output.String(); strings.Contains(got, "Found the alert source in util-rancid/update_cerner_daily.sh:753.") {
		t.Fatalf("provisional plain scout output should be suppressed during retry, got %q", got)
	}
}

func TestSubAgentStructuredOutputNudgeMessageForScoutAllowsContinuingWithTools(t *testing.T) {
	got := subAgentStructuredOutputNudgeMessage("scout", 1)
	for _, want := range []string{
		"call the next search/read/run_command tool now",
		"exactly one JSON object",
		"No prose outside tool calls or the JSON object",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("scout structured-output nudge missing %q: %q", want, got)
		}
	}
}

func TestSpawnSubAgentArchitectRetriesPlainFinalOutputIntoTypedEnvelope(t *testing.T) {
	driver := &inspectingDriver{
		checks: []func([]llm.Message) error{
			nil,
			func(messages []llm.Message) error {
				if len(messages) == 0 {
					return fmt.Errorf("missing retry message")
				}
				if got := messages[len(messages)-1].Content; !strings.Contains(got, "exactly one JSON object") {
					return fmt.Errorf("expected structured-output retry nudge, got %q", got)
				}
				return nil
			},
		},
		responses: []string{
			"The alert means the runtime verification helper was missing, not that the job failed.",
			`{"status":"complete","message":"The alert means the runtime verification helper was missing, not that the job failed.","artifact_kind":"plan","artifact":"The alert means the runtime verification helper was missing, not that the job failed.","next_role":"","next_task":""}`,
		},
	}

	reg := tools.NewRegistry()
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	parent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)

	result, err := parent.SpawnSubAgent(context.Background(), "architect", "TASK: Explain what the alert means in plain language. OUTCOME: Clear meaning only. CONTEXT: The scout already found the source path and trigger. MUST NOT: Do not inspect more files.", MultiAgentConfig{
		BaseTools: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 2 {
		t.Fatalf("expected plain architect output to trigger a retry, got %d driver calls", driver.callIdx)
	}
	if outcome := parseDelegateOutcome(result); !outcome.Structured || outcome.Message != "The alert means the runtime verification helper was missing, not that the job failed." {
		t.Fatalf("expected typed architect result after retry, got %q", result)
	}
}

func TestSubAgentNeedsStructuredRetryRequiresExactEnvelopeForScout(t *testing.T) {
	if !subAgentNeedsStructuredRetry("scout", `{"source_file":"util-rancid/update_cerner_daily.sh","source_line":753,"message":"Found the alert source.","evidence":["mailx subject matches"]}`) {
		t.Fatal("expected bare scout json to require another structured-output retry")
	}
	if subAgentNeedsStructuredRetry("scout", `{"status":"complete","message":"Found the alert source.","artifact_kind":"evidence","artifact":"{\"source_file\":\"util-rancid/update_cerner_daily.sh\",\"source_line\":753}","next_role":"","next_task":""}`) {
		t.Fatal("expected exact scout envelope to satisfy the retry gate")
	}
}

func TestSpawnSubAgentArchitectNormalizesBareJSONObjectIntoTypedEnvelopeWithoutRetry(t *testing.T) {
	driver := &inspectingDriver{
		responses: []string{
			`{"severity":"medium","likely_impact":"Verification coverage gap","suggested_next_checks":["confirm script path"]}`,
		},
	}

	reg := tools.NewRegistry()
	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	parent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)

	result, err := parent.SpawnSubAgent(context.Background(), "architect", "TASK: Assess whether the alert is urgent. OUTCOME: Concise assessment only. CONTEXT: Prior source and trigger are already known. MUST NOT: Do not inspect more files.", MultiAgentConfig{
		BaseTools: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 1 {
		t.Fatalf("expected bare architect json to be normalized locally, got %d driver calls", driver.callIdx)
	}
	if outcome := parseDelegateOutcomeForRole("architect", result); !outcome.Structured || outcome.Message != "Severity: Medium. Next check: confirm script path." {
		t.Fatalf("expected coerced architect result, got %q", result)
	}
}

func TestDispatchRewritesRepoReviewScoutTaskToEvidenceOnly(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Inspect the repository and gather evidence about its purpose, structure, tech stack, key modules, and obvious cleanup/maintenance opportunities. OUTCOME: A concise but concrete repo review with file/path references, including recommended cleanup actions. CONTEXT: User asked: 'explain this repo and recommend and cleanup actions this might need'. MUST NOT: Do not modify files. Do not make guesses without citing evidence from the repo.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	var scoutTask string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			task, _ := args["task"].(string)
			delegated = append(delegated, role)
			switch role {
			case "scout":
				scoutTask = task
				return `{"status":"complete","message":"Scout evidence gathered.","artifact_kind":"evidence","artifact":"FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests","next_role":"architect","next_task":"TASK: Synthesize the repo review into prioritized recommendations. OUTCOME: Final review."}`, nil
			case "architect":
				return `{"status":"complete","message":"Recommendations ready.","artifact_kind":"plan","artifact":"Prioritized recommendations","next_role":"","next_task":""}`, nil
			default:
				return "", fmt.Errorf("unexpected role %q", role)
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "explain this repo and recommend and cleanup actions this might need"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if strings.Contains(strings.ToLower(scoutTask), "recommended cleanup actions") {
		t.Fatalf("scout task should be rewritten to evidence-only, got %q", scoutTask)
	}
	if !strings.Contains(scoutTask, "Evidence-backed findings only") {
		t.Fatalf("scout task missing evidence-only outcome: %q", scoutTask)
	}
	if !strings.Contains(scoutTask, "Do not provide final recommendations") {
		t.Fatalf("scout task missing no-recommendations constraint: %q", scoutTask)
	}
}

func TestScoutFiltersRuntimeArtifactsFromSearchResultsByDefault(t *testing.T) {
	driver := &inspectingDriver{
		responses: []string{
			"<tool_call>\n{\"name\": \"search\", \"args\": {\"pattern\": \"Rancid f5 objstor verify script missing\", \"path\": \".\"}}\n</tool_call>",
			"FINDINGS:\n- found source\nKEY FILES: /repo/util-rancid/update_cerner_daily.sh\nFOLLOW-UP: none\nUNKNOWNS: none",
		},
		checks: []func([]llm.Message) error{
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				for _, forbidden := range []string{
					"artifact-log.jsonl",
					"runtime-owned-log.jsonl",
					".forge/scratchpad/email_origin_investigation_raw.md",
					"history.jsonl",
					"sessions/run-123/log/agent.log",
				} {
					if strings.Contains(joined, forbidden) {
						return fmt.Errorf("runtime artifact leaked into scout context: %s", forbidden)
					}
				}
				if !strings.Contains(joined, "util-rancid/update_cerner_daily.sh:753") {
					return fmt.Errorf("real source hit missing from scout context: %s", joined)
				}
				return nil
			},
		},
	}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "search",
		Description: "Search",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return strings.Join([]string{
				"./artifact-log.jsonl:2:{\"msg\":\"chat.input\"}",
				"./runtime-owned-log.jsonl:3:{\"msg\":\"llm.request\"}",
				"./.forge/scratchpad/email_origin_investigation_raw.md:1:cached prior finding",
				"./history.jsonl:8:{\"msg\":\"session history\"}",
				"./sessions/run-123/log/agent.log:4:delegating to scout",
				"./util-rancid/update_cerner_daily.sh:753:\trun_or_warn \"f5 objstor verify missing-script alert email\" mailx -s \"Rancid f5 objstor verify script missing\" martin.cassidy@oracle.com </dev/null",
			}, "\n"), nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("scout")
	a.isSubAgent = true

	if err := a.Run(context.Background(), "Investigate where the email came from in this codebase."); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlySubAgentsFilterRuntimeArtifactsFromSearchResultsByDefault(t *testing.T) {
	for _, role := range []string{"architect", "doctor"} {
		t.Run(role, func(t *testing.T) {
			driver := &inspectingDriver{
				responses: []string{
					"<tool_call>\n{\"name\": \"search\", \"args\": {\"pattern\": \"Rancid f5 objstor verify script missing\", \"path\": \".\"}}\n</tool_call>",
					"done",
				},
				checks: []func([]llm.Message) error{
					nil,
					func(messages []llm.Message) error {
						joined := ""
						for _, msg := range messages {
							joined += msg.Content + "\n"
						}
						for _, forbidden := range []string{
							"artifact-log.jsonl",
							"runtime-owned-log.jsonl",
							".forge/scratchpad/email_origin_investigation_raw.md",
							"history.jsonl",
							"sessions/run-123/log/agent.log",
						} {
							if strings.Contains(joined, forbidden) {
								return fmt.Errorf("runtime artifact leaked into %s context: %s", role, forbidden)
							}
						}
						if !strings.Contains(joined, "util-rancid/update_cerner_daily.sh:753") {
							return fmt.Errorf("real source hit missing from %s context: %s", role, joined)
						}
						return nil
					},
				},
			}
			reg := tools.NewRegistry()
			reg.Register(tools.Tool{
				Name:        "search",
				Description: "Search",
				Execute: func(ctx context.Context, args map[string]any) (string, error) {
					return strings.Join([]string{
						"./artifact-log.jsonl:2:{\"msg\":\"chat.input\"}",
						"./runtime-owned-log.jsonl:3:{\"msg\":\"llm.request\"}",
						"./.forge/scratchpad/email_origin_investigation_raw.md:1:cached prior finding",
						"./history.jsonl:8:{\"msg\":\"session history\"}",
						"./sessions/run-123/log/agent.log:4:delegating to scout",
						"./util-rancid/update_cerner_daily.sh:753:\trun_or_warn \"f5 objstor verify missing-script alert email\" mailx -s \"Rancid f5 objstor verify script missing\" martin.cassidy@oracle.com </dev/null",
					}, "\n"), nil
				},
			})

			var output bytes.Buffer
			renderer := NewRenderer(&output, 80, false)
			a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
			a.SetRole(role)
			a.isSubAgent = true

			if err := a.Run(context.Background(), "Investigate where the email came from in this codebase."); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScoutAllowsRuntimeArtifactsWhenTaskExplicitlyRequestsDebugLogInspection(t *testing.T) {
	driver := &inspectingDriver{
		responses: []string{
			"<tool_call>\n{\"name\": \"search\", \"args\": {\"pattern\": \"delegate\", \"path\": \".\"}}\n</tool_call>",
			"FINDINGS:\n- debug log inspected\nKEY FILES: /repo/artifact-log.jsonl\nFOLLOW-UP: none\nUNKNOWNS: none",
		},
		checks: []func([]llm.Message) error{
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				if !strings.Contains(joined, "artifact-log.jsonl") {
					return fmt.Errorf("explicit debug-log task should preserve debug artifact hits")
				}
				return nil
			},
		},
	}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "search",
		Description: "Search",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return strings.Join([]string{
				"./artifact-log.jsonl:2:{\"msg\":\"chat.input\"}",
				"./util-rancid/update_cerner_daily.sh:753:\trun_or_warn \"f5 objstor verify missing-script alert email\"",
			}, "\n"), nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("scout")
	a.isSubAgent = true

	if err := a.Run(context.Background(), "Inspect the debug log and tell me why dispatch delegated twice."); err != nil {
		t.Fatal(err)
	}
}

func TestSubAgentKeepsFreshToolResultsUnclippedBetweenTurns(t *testing.T) {
	const marker = "VERY_IMPORTANT_MARKER_AT_END"

	driver := &inspectingDriver{
		responses: []string{
			"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"alert-source.sh\"}}\n</tool_call>",
			"FINDINGS:\n- preserved full tool output\nKEY FILES: /repo/alert-source.sh\nFOLLOW-UP: none\nUNKNOWNS: none",
		},
		checks: []func([]llm.Message) error{
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				if !strings.Contains(joined, marker) {
					return fmt.Errorf("fresh tool result marker missing from second turn context: %s", joined)
				}
				return nil
			},
		},
	}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "read_file",
		Description: "Read file",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return strings.Repeat("abcdefghij", 30) + marker, nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("scout")
	a.isSubAgent = true

	if err := a.Run(context.Background(), "Inspect the alert source and return evidence-backed findings."); err != nil {
		t.Fatal(err)
	}
}

func TestScoutRetriesBlockedAnswerUntilItUsesEvidenceTools(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"I couldn’t verify the origin yet. If you want, I can do a narrow repository search next.",
		"<tool_call>\n{\"name\": \"search\", \"args\": {\"pattern\": \"Rancid f5 objstor verify script missing\", \"path\": \".\"}}\n</tool_call>",
		"FINDINGS:\n- /repo/util-rancid/update_cerner_daily.sh:753 sends the alert email\nKEY FILES: /repo/util-rancid/update_cerner_daily.sh\nFOLLOW-UP: none\nUNKNOWNS: none",
	}}
	reg := tools.NewRegistry()
	var searchCalls int
	reg.Register(tools.Tool{
		Name:        "search",
		Description: "Search",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			searchCalls++
			return "/repo/util-rancid/update_cerner_daily.sh:753: run_or_warn \"f5 objstor verify missing-script alert email\"", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("scout")
	a.isSubAgent = true

	if err := a.Run(context.Background(), "TASK: Trace the origin of the email subject and identify why it would be sent. OUTCOME: Evidence-backed findings with file path and triggering condition. MUST NOT: Do not speculate."); err != nil {
		t.Fatal(err)
	}
	if searchCalls != 1 {
		t.Fatalf("expected scout to be nudged into at least one evidence tool call, got %d search calls", searchCalls)
	}
	if driver.callIdx != 3 {
		t.Fatalf("expected blocked scout answer to be retried before final findings, got %d driver calls", driver.callIdx)
	}
	if got := output.String(); strings.Contains(got, "If you want, I can do a narrow repository search next.") {
		t.Fatalf("blocked scout prose should not be rendered when tools are still available: %q", got)
	}
}

func TestScoutRepoReviewNoOutputGetsTargetedEvidenceNudge(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Forge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module forge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "BUILD.md"), []byte("# Build\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "agent.go"), []byte("package agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	driver := &inspectingDriver{
		responses: []string{
			"<tool_call>\n{\"name\": \"list_dir\", \"args\": {\"path\": \".\", \"recursive\": false}}\n</tool_call>",
			"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"README.md\", \"start_line\": 1, \"end_line\": 40}}\n</tool_call>",
			"",
			"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"go.mod\", \"start_line\": 1, \"end_line\": 40}}\n</tool_call>",
			"<tool_call>\n{\"name\": \"list_dir\", \"args\": {\"path\": \"internal\", \"recursive\": false}}\n</tool_call>",
			"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"BUILD.md\", \"start_line\": 1, \"end_line\": 40}}\n</tool_call>",
			`{"status":"complete","message":"Repo evidence gathered.","artifact_kind":"evidence","artifact":"FINDINGS:\n- README.md explains the CLI.\n- go.mod defines the module.\n- internal/ holds agent internals.\n- BUILD.md documents build flow.","next_role":"","next_task":""}`,
		},
		checks: []func([]llm.Message) error{
			nil,
			nil,
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				for _, want := range []string{
					"Repo-review evidence is still incomplete.",
					"go.mod",
					"internal",
					"BUILD.md",
				} {
					if !strings.Contains(joined, want) {
						return fmt.Errorf("missing targeted repo-review nudge content %q in messages: %s", want, joined)
					}
				}
				return nil
			},
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	a.SetRole("scout")
	a.isSubAgent = true

	if err := a.Run(context.Background(), "TASK: Inspect the Go repository and gather evidence about its purpose, structure, main packages/binaries, dependencies, test/build health indicators, and obvious cleanup opportunities. OUTCOME: Evidence-backed findings only."); err != nil {
		t.Fatal(err)
	}
}

func TestScoutRepoReviewBlockedAnswerRetriesUntilChecklistCovered(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Forge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module forge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "BUILD.md"), []byte("# Build\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "agent.go"), []byte("package agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {\"path\": \".\", \"recursive\": false}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"README.md\", \"start_line\": 1, \"end_line\": 40}}\n</tool_call>",
		"I’m blocked from giving the requested evidence-backed repository findings because I still need to inspect go.mod, internal/*, and BUILD.md before concluding.",
		"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"go.mod\", \"start_line\": 1, \"end_line\": 40}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"list_dir\", \"args\": {\"path\": \"internal\", \"recursive\": false}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"BUILD.md\", \"start_line\": 1, \"end_line\": 40}}\n</tool_call>",
		`{"status":"complete","message":"Repo evidence gathered.","artifact_kind":"evidence","artifact":"FINDINGS:\n- README.md explains the CLI.\n- go.mod defines the module.\n- internal/ holds agent internals.\n- BUILD.md documents build flow.","next_role":"","next_task":""}`,
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))
	reg.Register(tools.NewReadFile(dir))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	a.SetRole("scout")
	a.isSubAgent = true

	if err := a.Run(context.Background(), "TASK: Inspect the Go repository and gather evidence about its purpose, structure, main packages/binaries, dependencies, test/build health indicators, and obvious cleanup opportunities. OUTCOME: Evidence-backed findings only."); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 7 {
		t.Fatalf("expected blocked repo-review answer to be retried until checklist was covered, got %d driver calls", driver.callIdx)
	}
	if got := output.String(); strings.Contains(got, "I’m blocked from giving the requested evidence-backed repository findings") {
		t.Fatalf("blocked repo-review prose should not be rendered when more evidence is still required: %q", got)
	}
}

func TestScoutRepoReviewBlockedAnswerRetriesWithoutTopLevelDiscovery(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Forge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module forge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "BUILD.md"), []byte("# Build\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "agent.go"), []byte("package agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	driver := &inspectingDriver{
		responses: []string{
			"<tool_call>\n{\"name\": \"git_log\", \"args\": {\"count\": 8}}\n</tool_call>",
			"<tool_call>\n{\"name\": \"glob\", \"args\": {\"pattern\": \"README.md\"}}\n</tool_call>",
			"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"README.md\", \"start_line\": 1, \"end_line\": 20}}\n</tool_call>",
			`{"status":"blocked","message":"Need manifest, source, and health evidence.","artifact_kind":"evidence","artifact":["README.md"],"next_role":"","next_task":""}`,
			"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"go.mod\", \"start_line\": 1, \"end_line\": 20}}\n</tool_call>",
			"<tool_call>\n{\"name\": \"list_dir\", \"args\": {\"path\": \"internal\", \"recursive\": false}}\n</tool_call>",
			"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"BUILD.md\", \"start_line\": 1, \"end_line\": 20}}\n</tool_call>",
			`{"status":"complete","message":"Repo evidence gathered.","artifact_kind":"evidence","artifact":"README.md, go.mod, internal/, BUILD.md","next_role":"","next_task":""}`,
		},
		checks: []func([]llm.Message) error{
			nil,
			nil,
			nil,
			nil,
			func(messages []llm.Message) error {
				joined := ""
				for _, msg := range messages {
					joined += msg.Content + "\n"
				}
				for _, want := range []string{
					"Repo-review evidence is still incomplete.",
					"list_dir on .",
					"manifest/source/health",
				} {
					if !strings.Contains(joined, want) {
						return fmt.Errorf("missing repo-review recovery nudge content %q in messages: %s", want, joined)
					}
				}
				return nil
			},
		},
	}

	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "git_log",
		Description: "Show recent commit history (git log --oneline).",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "01ffccc latest rancid hour/day script\n", nil
		},
	})
	reg.Register(tools.NewGlob(dir, nil))
	reg.Register(tools.NewReadFile(dir))
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), dir, 12, renderer, nil, nil)
	a.SetRole("scout")
	a.isSubAgent = true

	task := "TASK: Review the repository health and provide evidence-based findings on structure, code quality, testing/build setup, and obvious risks. OUTCOME: Evidence-backed findings only. Gather repository purpose, structure, tech stack, key modules, and concrete maintenance signals with file/path references so an architect can synthesize recommendations. MUST NOT: Modify files. Do not provide final recommendations, cleanup actions, prioritization, or user-facing advice. Read at least one relevant file or search result before concluding; do not base a repository review on directory listings alone."
	if err := a.Run(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 8 {
		t.Fatalf("expected repo-review scout to continue after partial blocked evidence, got %d calls", driver.callIdx)
	}
	if got := output.String(); strings.Contains(got, "Need manifest, source, and health evidence.") {
		t.Fatalf("blocked repo-review JSON should not be rendered while checklist is incomplete: %q", got)
	}
}

func TestArchitectRetriesInsteadOfMixingToolCallsWithVisibleProse(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"read_file\", \"args\": {\"path\": \"repo_review_evidence.md\"}}\n</tool_call>\nI need the actual scout evidence contents to synthesize recommendations.",
		"GOAL: prioritized recommendations",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "read_file",
		Description: "Read file",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "FINDINGS:\n- tests are sparse", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("architect")
	a.isSubAgent = true

	if err := a.Run(context.Background(), "synthesize repo review"); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 2 {
		t.Fatalf("expected architect retry flow, got %d driver calls", driver.callIdx)
	}
	if got := output.String(); strings.Contains(got, "I need the actual scout evidence contents") {
		t.Fatalf("architect prose leak should trigger retry, got %q", got)
	}
	for _, msg := range a.history {
		if strings.Contains(msg.Content, "I need the actual scout evidence contents") {
			t.Fatalf("architect mixed prose should not remain in history: %q", msg.Content)
		}
	}
}

func TestDispatchRetriesDirectAnswerUntilItDelegates(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"Repo overview\n- Purpose\n- Structure",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"find it\"}}\n</tool_call>",
		"Here are the results from scout.",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "scout found stuff", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "find the repo description"); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if strings.Contains(got, "Repo overview") {
		t.Fatalf("dispatch should not leak direct answers before delegating: %q", got)
	}
	if strings.Contains(got, "Here are the results from scout.") {
		t.Fatalf("dispatch should stay silent after delegation: %q", got)
	}
	if !strings.Contains(got, "scout found stuff") {
		t.Fatalf("delegate tool result missing from output: %q", got)
	}
	if driver.callIdx != 2 {
		t.Fatalf("expected dispatch retry and delegate without an extra stop turn, got %d driver calls", driver.callIdx)
	}
}

func TestDispatchAllowsRepeatedScoutDelegation(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"describe repo\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			switch len(delegated) {
			case 1:
				return `{"status":"complete","message":"Initial scout pass complete.","artifact_kind":"evidence","artifact":"FINDINGS:\n- scout found stuff\nKEY FILES: /repo/README.md","next_role":"scout","next_task":"look for problems"}`, nil
			case 2:
				return `{"status":"complete","message":"Follow-up scout pass complete.","artifact_kind":"evidence","artifact":"FINDINGS:\n- more scout findings\nKEY FILES: /repo/README.md","next_role":"architect","next_task":"synthesize the scout evidence"}`, nil
			default:
				return `{"status":"complete","message":"Prioritized recommendations ready.","artifact_kind":"plan","artifact":"GOAL: prioritized recommendations","next_role":"","next_task":""}`, nil
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review this repo and suggest improvements"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,scout,architect", got)
	}
}

func TestDispatchAllowsScoutArchitectScoutLoop(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"describe repo\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			switch len(delegated) {
			case 1:
				return `{"status":"complete","message":"Scout evidence gathered.","artifact_kind":"evidence","artifact":"scout result","next_role":"architect","next_task":"organize findings"}`, nil
			case 2:
				return `{"status":"complete","message":"Architect organized findings.","artifact_kind":"plan","artifact":"architect result","next_role":"scout","next_task":"look for more problems"}`, nil
			default:
				return `{"status":"complete","message":"Additional scout pass complete.","artifact_kind":"evidence","artifact":"more scout result","next_role":"","next_task":""}`, nil
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review repo and suggest improvements"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect,scout" {
		t.Fatalf("delegated roles = %q, want scout,architect,scout", got)
	}
}

func TestDispatchAllowsBuilderSelectionOnDebugRequestWithoutRuntimeVeto(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"builder\", \"task\": \"fix it\"}}\n</tool_call>",
		"",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			return "fixed", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "why is this happening"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "builder" {
		t.Fatalf("delegated roles = %q, want builder", got)
	}
	if strings.Contains(output.String(), "debug flow in need_diagnosis phase does not allow builder") {
		t.Fatalf("unexpected debug-phase veto: %q", output.String())
	}
}

func TestDispatchRoutesInterpretiveFollowUpToArchitectAfterScoutEvidence(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"trace the alert source\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"explain what the scout findings mean and what should happen next\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			if role == "scout" {
				return `{"status":"complete","message":"Found the source of the alert.","artifact_kind":"evidence","artifact":"alert comes from /repo/util-rancid/update_cerner_daily.sh:753","next_role":"","next_task":""}`, nil
			}
			return `{"status":"complete","message":"No code fix is indicated. Restore the missing runtime dependency.","artifact_kind":"analysis","artifact":"1. No code fix is indicated.\n2. Restore the missing runtime dependency.","next_role":"","next_task":""}`, nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "where did this alert come from"); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), "what should we do about that?"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if strings.Contains(output.String(), "what should we do about that?") {
		t.Fatalf("dispatch should not render the follow-up prompt: %q", output.String())
	}
}

func TestDispatchAutoChainsTypedDelegateNextRoleWithoutSecondDispatchTurn(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"trace the alert source\"}}\n</tool_call>",
		"This second dispatch turn should not happen.",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			switch role {
			case "scout":
				return `{"status":"complete","message":"Found the source of the alert.","artifact_kind":"evidence","artifact":"alert comes from /repo/util-rancid/update_cerner_daily.sh:753","next_role":"architect","next_task":"Explain what the scout evidence means in plain language using the evidence only."}`, nil
			case "architect":
				return `{"status":"complete","message":"This is a warning about missing verification coverage, not proof of a code change.","artifact_kind":"analysis","artifact":"No code fix is indicated by the evidence."}`, nil
			default:
				return "unexpected", nil
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "where did this alert come from and what does it mean"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if driver.callIdx != 1 {
		t.Fatalf("driver call count = %d, want 1", driver.callIdx)
	}
	if strings.Contains(output.String(), `{"status":"complete"`) {
		t.Fatalf("typed delegate envelope leaked to renderer output: %q", output.String())
	}
	if !strings.Contains(output.String(), "missing verification coverage") {
		t.Fatalf("expected final architect message in renderer output, got %q", output.String())
	}
}

func TestDispatchInjectsTypedScoutArtifactIntoLaterArchitectTask(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"trace the alert source\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"explain what it means\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var architectTask string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			task, _ := args["task"].(string)
			switch role {
			case "scout":
				return `{"status":"complete","message":"Found the source of the alert.","artifact_kind":"evidence","artifact":"alert comes from /repo/util-rancid/update_cerner_daily.sh:753","next_role":"","next_task":""}`, nil
			case "architect":
				architectTask = task
				return `{"status":"complete","message":"It is a warning, not a code change.","artifact_kind":"analysis","artifact":"No code fix is indicated."}`, nil
			default:
				return "unexpected", nil
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "where did this alert come from"); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), "what should we do about that?"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(architectTask, "alert comes from /repo/util-rancid/update_cerner_daily.sh:753") {
		t.Fatalf("architect task missing scout artifact: %q", architectTask)
	}
	if strings.Contains(architectTask, `{"status":"complete"`) {
		t.Fatalf("architect task should receive typed artifact, not raw json envelope: %q", architectTask)
	}
	if strings.Contains(output.String(), `{"status":"complete"`) {
		t.Fatalf("typed delegate envelope leaked to renderer output: %q", output.String())
	}
}

func TestDispatchRoutesPlainLanguageMeaningFollowUpToArchitect(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"trace the alert source\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"explain what the alert means in plain language\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"this retry should not happen\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			if role == "scout" {
				return `{"status":"complete","message":"Found the source of the alert.","artifact_kind":"evidence","artifact":"alert comes from /repo/util-rancid/update_cerner_daily.sh:753","next_role":"","next_task":""}`, nil
			}
			return `{"status":"complete","message":"The alert means the runtime verification helper was missing, not that production is down.","artifact_kind":"analysis","artifact":"The alert means the runtime verification helper was missing, not that production is down.","next_role":"","next_task":""}`, nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 2, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "where did this alert come from"); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), "i dont understand what the email means"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if driver.callIdx != 2 {
		t.Fatalf("driver call count = %d, want 2", driver.callIdx)
	}
	if strings.Contains(output.String(), "dispatch cannot delegate to architect twice in a row") {
		t.Fatalf("plain-language follow-up should stop after architect, got %q", output.String())
	}
}

func TestDispatchAllowsRepeatedArchitectFollowUpsAcrossTurns(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"trace the alert source\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"explain what the scout findings mean and what should happen next\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"decide whether the user is good to ignore this now\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"this turn should not happen\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			switch role {
			case "scout":
				return `{"status":"complete","message":"Found the source of the alert.","artifact_kind":"evidence","artifact":"alert comes from /repo/util-rancid/update_cerner_daily.sh:753","next_role":"","next_task":""}`, nil
			case "architect":
				return `{"status":"complete","message":"No code fix is indicated; confirm the runtime script exists and is executable.","artifact_kind":"analysis","artifact":"No code fix is indicated; confirm the runtime script exists and is executable.","next_role":"","next_task":""}`, nil
			default:
				return "unexpected", nil
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 2, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "where did this alert come from"); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), "do i need to fix something? if so how, do not change anything yet"); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), "so im good then ?"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect,architect", got)
	}
	if driver.callIdx != 3 {
		t.Fatalf("driver call count = %d, want 3", driver.callIdx)
	}
	if strings.Contains(output.String(), "dispatch cannot delegate to architect twice in a row") {
		t.Fatalf("architect follow-up should not trip same-role guard, got %q", output.String())
	}
}

func TestDispatchAllowsBuilderSelectionOnPlanRequestWithoutRuntimeVeto(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"builder\", \"task\": \"draft the plan\"}}\n</tool_call>",
		"",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			return "GOAL: plan", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "write up a remediation plan"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "builder" {
		t.Fatalf("delegated roles = %q, want builder", got)
	}
	if strings.Contains(output.String(), "plan flow in need_plan phase does not allow builder") {
		t.Fatalf("unexpected plan-phase veto: %q", output.String())
	}
}

func TestDispatchRewritesModelProvidedScoutRepoReviewTaskToEvidenceOnly(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Inspect the repository and identify practical improvement opportunities. OUTCOME: Recommended improvements.\"}}\n</tool_call>",
		"",
	}}
	reg := tools.NewRegistry()
	var scoutTask string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			scoutTask, _ = args["task"].(string)
			return "FINDINGS:\n- README is thin\nKEY FILES: /repo/README.md\nFOLLOW-UP: architect", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review this repo and suggest improvements"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(scoutTask, "Evidence-backed findings only") {
		t.Fatalf("dispatch should rewrite scout repo-review tasks to evidence-only, got %q", scoutTask)
	}
	if !strings.Contains(scoutTask, "Do not provide final recommendations") {
		t.Fatalf("dispatch should add no-recommendations constraint, got %q", scoutTask)
	}
	if strings.Contains(output.String(), "repo-review and improvement requests must use scout for evidence gathering only") {
		t.Fatalf("unexpected repo-review guard output: %q", output.String())
	}
}

func TestDispatchRewritesScoutRepoTourCleanupTaskToEvidenceOnly(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Inspect the Go repository structure, key packages, entrypoints, tests, tooling/config, and obvious maintenance smells. OUTCOME: A concise repo tour plus a concrete list of recommended cleanup actions grounded in files/packages found. CONTEXT: User asked: 'explain this repo and recommend cleanup actions this might need'. Working directory is the repo root. MUST NOT: Modify files; keep it read-only; do not speculate without pointing to repo evidence.\"}}\n</tool_call>",
		"",
	}}
	reg := tools.NewRegistry()
	var scoutTask string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			scoutTask, _ = args["task"].(string)
			return "FINDINGS:\n- README is thin\nKEY FILES: /repo/README.md\nFOLLOW-UP: architect", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "explain this repo and recommend cleanup actions this might need"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(scoutTask, "Evidence-backed findings only") {
		t.Fatalf("dispatch should rewrite scout repo-tour cleanup tasks to evidence-only, got %q", scoutTask)
	}
	if !strings.Contains(scoutTask, "Do not provide final recommendations") {
		t.Fatalf("dispatch should add no-recommendations constraint, got %q", scoutTask)
	}
	if strings.Contains(output.String(), "repo-review and improvement requests must use scout for evidence gathering only") {
		t.Fatalf("unexpected repo-review guard output: %q", output.String())
	}
}

func TestDispatchSearchFlowStopsAfterScoutAnswer(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"investigate the source of the alert\"}}\n</tool_call>",
		"This prose should never be generated because dispatch should stop after scout completes.",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			return `{"status":"complete","message":"Found the alert source.","artifact_kind":"evidence","artifact":"alert comes from /repo/script.sh:10","next_role":"","next_task":""}`, nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "i got an email today that said \"Rancid f5 objstor verify script missing\" where did it come from and why"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout" {
		t.Fatalf("delegated roles = %q, want scout", got)
	}
	if driver.callIdx != 1 {
		t.Fatalf("dispatch should stop after typed scout completion, got %d driver calls", driver.callIdx)
	}
	if strings.Contains(output.String(), "This prose should never be generated") {
		t.Fatalf("dispatch should stop instead of generating prose after scout search result: %q", output.String())
	}
}

func TestDispatchSearchFlowSurfacesWhyFromCurrentScoutContract(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"investigate the source of the alert\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return `{"source_file":"util-rancid/update_cerner_daily.sh","source_line":753,"message_subject":"Rancid f5 objstor verify script missing","emitter":"mailx invocation inside the RANCID daily update script","trigger_condition":"The f5 objstor verify step expected a verify script to exist, but the script was missing or unavailable when the job ran","why_sent":"To alert the configured recipient that the automated RANCID verification for the f5 objstor target could not run due to the missing verify script","recipient":"martin.cassidy@oracle.com","context":"This appears to be a maintenance/monitoring alert from a scheduled RANCID update workflow, not a user-initiated email.","evidence":"run_or_warn \"f5 objstor verify missing-script alert email\" mailx -s \"Rancid f5 objstor verify script missing\" martin.cassidy@oracle.com </dev/null"}`, nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), `i got an email today that said "Rancid f5 objstor verify script missing" where did it come from and why`); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	if !strings.Contains(got, "Source: util-rancid/update_cerner_daily.sh:753.") {
		t.Fatalf("missing source summary in output: %q", got)
	}
	if !strings.Contains(got, "Likely trigger: The f5 objstor verify step expected a verify script to exist, but the script was missing or unavailable when the job ran.") {
		t.Fatalf("missing trigger summary in output: %q", got)
	}
}

func TestDispatchAllowsEvidenceOnlyScoutTaskWhenContextMentionsChanges(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence for a repository review of this project and identify notable code quality, maintenance, testing, dependency, structure, and documentation issues or risks. OUTCOME: Raw evidence-based findings from inspecting the repo, including concrete file paths and examples, suitable for a follow-up recommendation synthesis. CONTEXT: User asked: 'take a look at this repo and let me know if there are any changes i should make'. MUST NOT: Do not provide final recommendations or synthesized advice; only gather evidence from the repository.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Synthesize recommendations from the scout report. OUTCOME: prioritized recommendations.\"}}\n</tool_call>",
		"Done.",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			if role == "scout" {
				return `{"status":"complete","message":"Scout evidence gathered.","artifact_kind":"evidence","artifact":"FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests","next_role":"architect","next_task":"TASK: Turn the scout findings into prioritized recommendations. OUTCOME: Prioritized recommendations."}`, nil
			}
			return `{"status":"complete","message":"Recommendations ready.","artifact_kind":"plan","artifact":"GOAL: prioritized recommendations","next_role":"","next_task":""}`, nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "take a look at this repo and let me know if there are any changes i should make"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if strings.Contains(output.String(), "repo-review and improvement requests must use scout for evidence gathering only") {
		t.Fatalf("evidence-only scout task should not be rejected because context mentions changes: %q", output.String())
	}
}

func TestDispatchInjectsScoutFindingsIntoArchitectTask(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence only.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var architectTask string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			task, _ := args["task"].(string)
			if role == "scout" {
				return `{"status":"complete","message":"Scout evidence gathered.","artifact_kind":"evidence","artifact":"FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests","next_role":"architect","next_task":"TASK: Turn the scout findings into prioritized recommendations. OUTCOME: Prioritized recommendations."}`, nil
			}
			architectTask = task
			return `{"status":"complete","message":"Recommendations ready.","artifact_kind":"plan","artifact":"GOAL: prioritize improvements","next_role":"","next_task":""}`, nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review this repo and suggest improvements"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(architectTask, "FINDINGS:\n- tests are sparse") {
		t.Fatalf("architect task missing scout findings: %q", architectTask)
	}
}

func TestDispatchDoesNotAutoPersistRepoReviewArtifactsToScratchpad(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence for a repository review only. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
		"",
	}}
	reg := tools.NewRegistry()
	var writes int
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "FINDINGS:\n- docs are thin\nKEY FILES: /repo/README.md\nFOLLOW-UP: architect", nil
		},
	})
	reg.Register(tools.Tool{
		Name:        "scratchpad_write",
		Description: "Write scratchpad",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			writes++
			return "written", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review this repo and suggest improvements"); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("dispatch should not auto-persist repo-review artifacts, got %d writes", writes)
	}
}

func TestDispatchAllowsArchitectRetryAfterScratchpadRead(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Synthesize the repo review. OUTCOME: Recommendations.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"scratchpad_read\", \"args\": {\"topic\": \"repo_review_evidence\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Synthesize the repo review. OUTCOME: Recommendations.\"}}\n</tool_call>",
		"Done.",
	}}
	reg := tools.NewRegistry()
	var architectCalls int
	var secondArchitectTask string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			architectCalls++
			task, _ := args["task"].(string)
			if architectCalls == 1 {
				return `{"status":"blocked","message":"Need the repo-review evidence before synthesizing recommendations.","artifact_kind":"plan","artifact":"Missing scout evidence.","next_role":"","next_task":""}`, nil
			}
			secondArchitectTask = task
			return `{"status":"complete","message":"Recommendations ready.","artifact_kind":"plan","artifact":"GOAL: prioritized recommendations","next_role":"","next_task":""}`, nil
		},
	})
	reg.Register(tools.Tool{
		Name:        "scratchpad_read",
		Description: "Read scratchpad",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "FINDINGS:\n- docs are thin\nKEY FILES: /repo/README.md\nFOLLOW-UP: architect", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review this repo"); err != nil {
		t.Fatal(err)
	}
	if architectCalls != 2 {
		t.Fatalf("expected architect to run twice when dispatch retries after scratchpad read, got %d calls", architectCalls)
	}
	if !strings.Contains(secondArchitectTask, "FINDINGS:\n- docs are thin") {
		t.Fatalf("retry architect task missing scratchpad findings: %q", secondArchitectTask)
	}
}

func TestDispatchAllowsBuilderRetryAfterTypedBlockedResult(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"builder\", \"task\": \"TASK: Write the recovered remediation plan to improvments_cleanup.md. OUTCOME: file exists.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"builder\", \"task\": \"TASK: Write the recovered remediation plan to improvments_cleanup.md. OUTCOME: file exists.\"}}\n</tool_call>",
		"Done.",
	}}
	reg := tools.NewRegistry()
	var builderCalls int
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			builderCalls++
			if builderCalls == 1 {
				return `{"status":"blocked","message":"Need the earlier plan content before writing the file.","artifact_kind":"implementation","artifact":"Missing the previously generated remediation plan text needed to write improvments_cleanup.md.","next_role":"","next_task":""}`, nil
			}
			return `{"status":"complete","message":"file written","artifact_kind":"implementation","artifact":"improvments_cleanup.md written successfully.","next_role":"","next_task":""}`, nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "write that prior plan into a file"); err != nil {
		t.Fatal(err)
	}
	if builderCalls != 2 {
		t.Fatalf("expected builder to be retried after typed blocked result, got %d calls", builderCalls)
	}
}

func TestDispatchCarriesPriorArchitectResultIntoLaterBuilderTurn(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Write a remediation plan. OUTCOME: markdown plan.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"builder\", \"task\": \"TASK: Create improvments_cleanup.md with the previously recovered remediation plan text. OUTCOME: file exists.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var builderTask string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			task, _ := args["task"].(string)
			if role == "architect" {
				return `{"status":"complete","message":"Plan ready.","artifact_kind":"plan","artifact":"# Repository Cleanup and Contribution Readiness Plan\n\nExact remediation content.","next_role":"","next_task":""}`, nil
			}
			builderTask = task
			return "file written", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "write up a remediation plan"); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), "write that prior plan into improvments_cleanup.md"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(builderTask, "# Repository Cleanup and Contribution Readiness Plan") {
		t.Fatalf("builder task missing prior architect result: %q", builderTask)
	}
}

func TestDispatchExecutesOnlyFirstToolCallPerTurn(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"find evidence\"}}\n</tool_call>\n<tool_call>\n{\"name\": \"scratchpad_write\", \"args\": {\"topic\": \"ignored\", \"content\": \"invented\"}}\n</tool_call>\n<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"should not run yet\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	var scratchWrites int
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			if role == "scout" {
				return `{"status":"complete","message":"Scout evidence gathered.","artifact_kind":"evidence","artifact":"FINDINGS:\n- evidence","next_role":"architect","next_task":"now synthesize"}`, nil
			}
			return `{"status":"complete","message":"Synthesis ready.","artifact_kind":"plan","artifact":"GOAL: synthesize","next_role":"","next_task":""}`, nil
		},
	})
	reg.Register(tools.Tool{
		Name:        "scratchpad_write",
		Description: "Write scratchpad",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			scratchWrites++
			return "written", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review repo"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if scratchWrites != 0 {
		t.Fatalf("dispatch should ignore later same-turn tool calls, got %d scratchpad writes", scratchWrites)
	}
}

func TestDispatchRejectsSynthesizedScratchpadWriteContent(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"scratchpad_write\", \"args\": {\"topic\": \"repo-review-scout-findings\", \"content\": \"Scout findings to carry into recommendation synthesis:\\n- invented summary\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Synthesize recommendations.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var scratchWrites int
	var architectTask string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			task, _ := args["task"].(string)
			if role == "scout" {
				return `{"status":"complete","message":"Scout evidence gathered.","artifact_kind":"evidence","artifact":"FINDINGS:\n- real evidence\nKEY FILES: /repo/README.md","next_role":"","next_task":""}`, nil
			}
			architectTask = task
			return `{"status":"complete","message":"Recommendations ready.","artifact_kind":"plan","artifact":"GOAL: prioritized recommendations","next_role":"","next_task":""}`, nil
		},
	})
	reg.Register(tools.Tool{
		Name:        "scratchpad_write",
		Description: "Write scratchpad",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			scratchWrites++
			return "written", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review repo"); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), "keep going with the repo review"); err != nil {
		t.Fatal(err)
	}
	if scratchWrites != 0 {
		t.Fatalf("dispatch should reject synthesized scratchpad writes, got %d writes", scratchWrites)
	}
	if !strings.Contains(architectTask, "FINDINGS:\n- real evidence") {
		t.Fatalf("architect task missing real scout evidence: %q", architectTask)
	}
	if strings.Contains(architectTask, "invented summary") {
		t.Fatalf("architect task should not include synthesized dispatch summary: %q", architectTask)
	}
}

func TestDispatchAutoChainsInterpretiveScoutResultToArchitect(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Inspect the alert source. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
		"This prose should never be generated because dispatch should stop after the architect result.",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			switch role {
			case "scout":
				return `{"status":"complete","message":"Evidence gathered.","artifact_kind":"evidence","artifact":{"source_file":"/repo/util-rancid/update_cerner_daily.sh","source_line":753,"most_likely_trigger":"missing verify script at runtime"},"next_role":"","next_task":""}`, nil
			case "architect":
				return `{"status":"complete","message":"Architect output ready.","artifact_kind":"plan","artifact":{"worry_level":"low_to_medium","actionability":"actionable","recommended_next_check":"Verify the expected verify script path exists and is executable."},"next_role":"","next_task":""}`, nil
			default:
				return "", fmt.Errorf("unexpected role %q", role)
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "is this something i need to worry about?"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if driver.callIdx != 1 {
		t.Fatalf("dispatch should stop after the auto-chained architect result, got %d driver calls", driver.callIdx)
	}
	if got := output.String(); !strings.Contains(got, "Severity: Low to medium. Next check: Verify the expected verify script path exists and is executable.") {
		t.Fatalf("missing architect summary in output: %q", got)
	}
}

func TestDispatchDoesNotAutoChainTraceScoutResultToArchitect(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Trace the alert source. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			return `{"status":"complete","message":"Evidence gathered.","artifact_kind":"evidence","artifact":{"source_file":"/repo/util-rancid/update_cerner_daily.sh","source_line":753,"most_likely_trigger":"missing verify script at runtime"},"next_role":"","next_task":""}`, nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "where does this alert come from?"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout" {
		t.Fatalf("delegated roles = %q, want scout", got)
	}
	if got := output.String(); strings.Contains(got, "severity") {
		t.Fatalf("trace flow should not auto-chain to architect, got %q", got)
	}
}

func TestDispatchReusesImmediatelyPriorScoutArtifactForInterpretiveFollowUp(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Trace the alert source. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Re-check the current alert so we can decide how urgent it is. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	var architectTask string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			task, _ := args["task"].(string)
			delegated = append(delegated, role)
			switch role {
			case "scout":
				return `{"status":"complete","message":"Evidence gathered.","artifact_kind":"evidence","artifact":{"source_file":"/repo/util-rancid/update_cerner_daily.sh","source_line":753,"most_likely_trigger":"missing verify script at runtime"},"next_role":"","next_task":""}`, nil
			case "architect":
				architectTask = task
				return `{"status":"complete","message":"Architect output ready.","artifact_kind":"plan","artifact":{"worry_level":"low_to_medium","actionability":"actionable","recommended_next_check":"Verify the expected verify script path exists and is executable."},"next_role":"","next_task":""}`, nil
			default:
				return "", fmt.Errorf("unexpected role %q", role)
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "where does this alert come from?"); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), "should I worry about that?"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if !strings.Contains(architectTask, `"/repo/util-rancid/update_cerner_daily.sh"`) || !strings.Contains(architectTask, `"source_line":753`) {
		t.Fatalf("architect task missing reused scout evidence: %q", architectTask)
	}
	if got := output.String(); !strings.Contains(got, "Severity: Low to medium. Next check: Verify the expected verify script path exists and is executable.") {
		t.Fatalf("missing interpretive follow-up summary in output: %q", got)
	}
}

func TestDispatchTreatsTerseReferentialFollowUpAsInterpretive(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Trace the alert source. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Re-check the current alert so we can answer the user's follow-up. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	var architectTask string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			task, _ := args["task"].(string)
			delegated = append(delegated, role)
			switch role {
			case "scout":
				return `{"status":"complete","message":"Evidence gathered.","artifact_kind":"evidence","artifact":{"source_file":"/repo/util-rancid/update_cerner_daily.sh","source_line":753,"most_likely_trigger":"missing verify script at runtime"},"next_role":"","next_task":""}`, nil
			case "architect":
				architectTask = task
				return `{"status":"complete","message":"Architect output ready.","artifact_kind":"plan","artifact":{"severity":"medium","actionability":"high"},"next_role":"","next_task":""}`, nil
			default:
				return "", fmt.Errorf("unexpected role %q", role)
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "where does this alert come from?"); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), "well is it?"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if !strings.Contains(architectTask, `"/repo/util-rancid/update_cerner_daily.sh"`) || !strings.Contains(architectTask, `"source_line":753`) {
		t.Fatalf("architect task missing reused scout evidence: %q", architectTask)
	}
	if got := output.String(); !strings.Contains(got, "Severity: Medium. Actionability: High.") {
		t.Fatalf("missing terse follow-up architect summary in output: %q", got)
	}
}

func TestDispatchFallsBackToScoutEvidenceWhenAutoChainedArchitectBlocks(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Inspect the alert source. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			switch role {
			case "scout":
				return `{"status":"complete","message":"Evidence gathered.","artifact_kind":"evidence","artifact":{"source_file":"/repo/util-rancid/update_cerner_daily.sh","source_line":753,"most_likely_trigger":"missing verify script at runtime"},"next_role":"","next_task":""}`, nil
			case "architect":
				return `{"status":"blocked","message":"Need the verify script output before I can assess urgency.","artifact_kind":"plan","artifact":"","next_role":"","next_task":""}`, nil
			default:
				return "", fmt.Errorf("unexpected role %q", role)
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "is this something i need to worry about?"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	got := output.String()
	if !strings.Contains(got, "Source: /repo/util-rancid/update_cerner_daily.sh:753. Likely trigger: missing verify script at runtime.") {
		t.Fatalf("missing scout fallback summary in output: %q", got)
	}
	if !strings.Contains(got, "Interpretation unavailable") {
		t.Fatalf("missing interpretation-unavailable note in output: %q", got)
	}
}

func TestDispatchAllowsArchitectRepoReviewChoiceWithoutRuntimeVeto(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Synthesize the repo review into prioritized recommendations. OUTCOME: Final review.\"}}\n</tool_call>",
		"",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			return "Prioritized recommendations", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review this repo"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "architect" {
		t.Fatalf("delegated roles = %q, want architect", got)
	}
	if strings.Contains(output.String(), "assess_codebase flow in need_evidence phase does not allow architect") {
		t.Fatalf("unexpected architect repo-review veto: %q", output.String())
	}
}

func TestDispatchAllowsBuilderRepoReviewEvidenceCollectionWhenDispatchChoosesIt(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"builder\", \"task\": \"TASK: Gather concrete repository evidence for a repo review and write the raw findings into the shared scratchpad file repo_review_evidence.md so that an architect can synthesize recommendations. OUTCOME: Evidence-based raw findings.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			switch role {
			case "builder":
				return `{"status":"complete","message":"Builder evidence pass complete.","artifact_kind":"implementation","artifact":"Raw repo-review evidence captured.","next_role":"scout","next_task":"TASK: Gather evidence only for a repo review. OUTCOME: Evidence-backed findings only."}`, nil
			case "scout":
				return `{"status":"complete","message":"Scout evidence gathered.","artifact_kind":"evidence","artifact":"FINDINGS:\n- docs are thin\nKEY FILES: /repo/README.md","next_role":"architect","next_task":"TASK: Synthesize recommendations from the scout findings. OUTCOME: prioritized recommendations."}`, nil
			default:
				return `{"status":"complete","message":"Recommendations ready.","artifact_kind":"plan","artifact":"Prioritized recommendations","next_role":"","next_task":""}`, nil
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review this repo"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "builder,scout,architect" {
		t.Fatalf("delegated roles = %q, want builder,scout,architect", got)
	}
}

func TestDispatchStopsAfterSuccessfulArchitectRepoReviewSynthesis(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence only.\"}}\n</tool_call>",
		"Based on the repo review, the main improvements to make are: ...",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			if role == "scout" {
				return `{"status":"complete","message":"Scout evidence gathered.","artifact_kind":"evidence","artifact":"FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests","next_role":"architect","next_task":"TASK: Synthesize the repo review into prioritized recommendations. OUTCOME: Final review."}`, nil
			}
			return `{"status":"complete","message":"Recommendations ready.","artifact_kind":"plan","artifact":"Priority recommendations","next_role":"","next_task":""}`, nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review repo improvements"); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 1 {
		t.Fatalf("dispatch should stop after architect synthesis without an extra stop turn, got %d driver calls", driver.callIdx)
	}
	if strings.Contains(output.String(), "Based on the repo review") {
		t.Fatalf("dispatch should not emit direct final prose after architect result: %q", output.String())
	}
}

func TestDispatchStopsAfterArchitectRepoReviewRecommendationTaskWithoutSynthesizeKeyword(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence only.\"}}\n</tool_call>",
		"",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			if role == "scout" {
				return `{"status":"complete","message":"Scout evidence gathered.","artifact_kind":"evidence","artifact":"FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests","next_role":"architect","next_task":"TASK: Review the scout evidence in scratchpad and produce prioritized repository improvement recommendations for the user. OUTCOME: Concise, actionable list of improvements with rationale and priority, based only on repository evidence."}`, nil
			}
			return `{"status":"complete","message":"Recommendations ready.","artifact_kind":"plan","artifact":"Prioritized improvement recommendations","next_role":"","next_task":""}`, nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review repo improvements"); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 1 {
		t.Fatalf("dispatch should stop after architect recommendation task without an extra stop turn, got %d driver calls", driver.callIdx)
	}
}

func TestDispatchFailsClosedWhenItNeverDelegates(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"Repo overview\n- Purpose",
		"Still summarizing the repo.",
	}}
	reg := tools.NewRegistry()

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 2, renderer, nil, nil)
	a.SetRole("dispatch")

	err := a.Run(context.Background(), "describe this repo")
	if err == nil || !strings.Contains(err.Error(), "dispatch") {
		t.Fatalf("expected dispatch failure, got %v", err)
	}
	if got := output.String(); strings.Contains(got, "Repo overview") || strings.Contains(got, "Still summarizing") {
		t.Fatalf("dispatch should not render direct answers when it never delegates: %q", got)
	}
}

func TestAgentRunParsesCloseOnlyToolCallFragment(t *testing.T) {
	dir := t.TempDir()
	driver := &mockDriver{responses: []string{
		"{\"name\": \"list_dir\", \"args\": {\"path\": \".\", \"recursive\": false}}</tool_call>",
		"Done.",
	}}

	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(dir, nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), dir, 10, renderer, nil, nil)
	if err := agent.Run(context.Background(), "list files"); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if strings.Contains(got, "</tool_call>") || strings.Contains(got, `{"name": "list_dir"`) {
		t.Fatalf("raw malformed tool call leaked to renderer output: %q", got)
	}
}

func TestAgentRunDoesNotTruncateDelegateResult(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"review repo\"}}\n</tool_call>",
		"Done.",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			lines := make([]string, 0, 25)
			for i := 1; i <= 25; i++ {
				lines = append(lines, fmt.Sprintf("line %02d", i))
			}
			return strings.Join(lines, "\n"), nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	agent := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	agent.SetRole("dispatch")
	if err := agent.Run(context.Background(), "find the long delegate output"); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "line 25") {
		t.Fatalf("delegate output was truncated: %q", got)
	}
}

func TestDispatchRetriesScoutAfterMalformedInlineToolMarkupDelegateResult(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather repo-review evidence only. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Retry the repo-review evidence pass narrowly. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			switch len(delegated) {
			case 1:
				return "Reviewing the repo structure, tests, and config for concrete issues now.<tool_call>{\"name\":\"list_dir\",\"args\":{\"path\":\".\",\"recursive\":false}}</tool_call>", nil
			case 2:
				return `{"status":"complete","message":"Retry scout pass complete.","artifact_kind":"evidence","artifact":"FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests","next_role":"architect","next_task":"TASK: Synthesize the repo review into prioritized recommendations. OUTCOME: Final review."}`, nil
			default:
				return `{"status":"complete","message":"Recommendations ready.","artifact_kind":"plan","artifact":"Prioritized recommendations","next_role":"","next_task":""}`, nil
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review this repo and tell me what to change"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,scout,architect", got)
	}
	if got := output.String(); strings.Contains(got, "<tool_call>") || strings.Contains(got, `{"name":"list_dir"`) {
		t.Fatalf("raw delegate tool markup leaked to renderer output: %q", got)
	}
}

func TestDispatchRetriesScoutAfterEmptyDelegateOutputWithoutPersistingSentinel(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather repo-review evidence only. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Retry the repo-review evidence pass narrowly. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	var scratchWrites int
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			switch len(delegated) {
			case 1:
				return "(sub-agent produced no output)", nil
			case 2:
				return `{"status":"complete","message":"Retry scout pass complete.","artifact_kind":"evidence","artifact":"FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests","next_role":"architect","next_task":"TASK: Synthesize the repo review into prioritized recommendations. OUTCOME: Final review."}`, nil
			default:
				return `{"status":"complete","message":"Recommendations ready.","artifact_kind":"plan","artifact":"Prioritized recommendations","next_role":"","next_task":""}`, nil
			}
		},
	})
	reg.Register(tools.Tool{
		Name:        "scratchpad_write",
		Description: "Write scratchpad",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			scratchWrites++
			return "written", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review this repo and tell me what to change"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,scout,architect", got)
	}
	if scratchWrites != 0 {
		t.Fatalf("dispatch should not auto-persist after empty scout output, got %d scratchpad writes", scratchWrites)
	}
	if strings.Contains(output.String(), "dispatch cannot delegate to scout twice in a row") {
		t.Fatalf("empty scout output should not poison dispatch state, got %q", output.String())
	}
}

func TestDispatchSuppressesBlockedScoutIntermediateResultBeforeRetry(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather repo-review evidence only. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Retry the repo-review evidence pass narrowly. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			switch role {
			case "scout":
				if len(delegated) == 1 {
					return `{"status":"blocked","message":"Evidence gathered so far is insufficient for a supported code-quality audit.","artifact_kind":"evidence","artifact":"Need implementation and test reads before concluding.","next_role":"","next_task":""}`, nil
				}
				return `{"status":"complete","message":"Retry scout pass complete.","artifact_kind":"evidence","artifact":"FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests","next_role":"","next_task":""}`, nil
			case "architect":
				return `{"status":"complete","message":"Recommendations ready.","artifact_kind":"plan","artifact":"GOAL: prioritize repo-review findings","next_role":"","next_task":""}`, nil
			default:
				return "", fmt.Errorf("unexpected role %q", role)
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "audit the repo for problems"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,scout,architect", got)
	}
	got := output.String()
	if strings.Contains(got, "Evidence gathered so far is insufficient for a supported code-quality audit.") {
		t.Fatalf("blocked scout retry result should stay internal, got %q", got)
	}
	if !strings.Contains(got, "Recommendations ready.") {
		t.Fatalf("architect synthesis missing after successful scout retry: %q", got)
	}
}

func TestDispatchAutoChainedArchitectTaskKeepsRepoReviewScopeAndRichScoutContext(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Audit the repository for problems. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
		"This prose should never be generated because dispatch should stop after the architect result.",
	}}
	reg := tools.NewRegistry()
	var architectTask string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			task, _ := args["task"].(string)
			switch role {
			case "scout":
				return `{"status":"complete","message":"Repository purpose: Forge is a Go-based terminal-first local coding agent. Evidence-backed findings: High severity - Go 1.25.0 requirement may constrain contributors. Medium severity - docs contain absolute /Users/cass/git/forge links that are not portable.","artifact_kind":"repo_review","artifact":["README.md","go.mod","BUILD.md"],"next_role":"architect","next_task":"Synthesize these evidence-backed findings into repo-review recommendations and risk prioritization."}`, nil
			case "architect":
				architectTask = task
				return `{"status":"complete","message":"Repo-review synthesis ready.","artifact_kind":"plan","artifact":"GOAL: prioritize repo-review findings","next_role":"","next_task":""}`, nil
			default:
				return "", fmt.Errorf("unexpected role %q", role)
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "audit the repo for problems"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(architectTask, "SCOPE: repo-review") {
		t.Fatalf("architect task should preserve repo-review scope: %q", architectTask)
	}
	if strings.Contains(architectTask, "SCOPE: single-file") {
		t.Fatalf("architect task should not be narrowed to single-file: %q", architectTask)
	}
	if !strings.Contains(architectTask, "High severity - Go 1.25.0 requirement may constrain contributors.") {
		t.Fatalf("architect task missing rich scout message context: %q", architectTask)
	}
	if !strings.Contains(architectTask, `["README.md","go.mod","BUILD.md"]`) {
		t.Fatalf("architect task missing scout artifact index: %q", architectTask)
	}
}

func TestDispatchAutoChainsRepoReviewScoutResultToArchitectWithoutExplicitNextRole(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Audit the repository for problems. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
		"This prose should never be generated because dispatch should stop after the architect result.",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	var architectTask string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			task, _ := args["task"].(string)
			delegated = append(delegated, role)
			switch role {
			case "scout":
				return `{"status":"complete","message":"Evidence-backed repo review completed with concrete file-backed findings covering repository purpose, structure, stack, key modules, and maintenance signals.","artifact_kind":"evidence","artifact":"1. go.mod requires Go 1.25.0.\n2. README.md contains absolute /Users/cass/git/forge links.\n3. cmd/forge/main.go concentrates CLI and session orchestration responsibilities.","next_role":"","next_task":""}`, nil
			case "architect":
				architectTask = task
				return `{"status":"complete","message":"Recommendations ready.","artifact_kind":"plan","artifact":"GOAL: prioritize repo-review findings","next_role":"","next_task":""}`, nil
			default:
				return "", fmt.Errorf("unexpected role %q", role)
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "audit the repo for problems"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if driver.callIdx != 1 {
		t.Fatalf("dispatch should stop after the auto-chained architect result, got %d driver calls", driver.callIdx)
	}
	if !strings.Contains(architectTask, "SCOPE: repo-review") {
		t.Fatalf("architect task should preserve repo-review scope: %q", architectTask)
	}
	if !strings.Contains(architectTask, "go.mod requires Go 1.25.0.") {
		t.Fatalf("architect task missing scout evidence: %q", architectTask)
	}
	if got := output.String(); !strings.Contains(got, "Recommendations ready.") {
		t.Fatalf("missing architect output in transcript: %q", got)
	}
}

func TestDispatchStopsAfterPlainCompletedDelegateWithoutExtraTurn(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Inspect the alert source and determine whether anything needs fixed. OUTCOME: Evidence-backed findings only. MUST NOT: Do not modify files.\"}}\n</tool_call>",
		"This prose should never be generated because dispatch should stop after a completed plain delegate result.",
	}}

	reg := tools.NewRegistry()
	var delegated []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			delegated = append(delegated, role)
			if role == "scout" {
				return "FINDINGS:\n- alert originates from /repo/util-rancid/update_cerner_daily.sh:753\nKEY FILES: /repo/util-rancid/update_cerner_daily.sh\nFOLLOW-UP: decide whether this is a code or operational issue\nUNKNOWNS: none", nil
			}
			return "1. No repo change indicated.\n2. Restore the missing runtime script or fix deployment/config.\n3. Change code only if the path check is stale.", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "help me understand what to do next"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout" {
		t.Fatalf("delegated roles = %q, want scout", got)
	}
	if driver.callIdx != 1 {
		t.Fatalf("dispatch should stop after plain completed delegate, got %d driver calls", driver.callIdx)
	}
	if strings.Contains(output.String(), "This prose should never be generated") {
		t.Fatalf("dispatch should not take a second model turn after plain completion, got %q", output.String())
	}
}

func TestCancelSubAgentSafe(t *testing.T) {
	a := &Agent{}
	// Should not panic when no sub-agent is active.
	a.CancelSubAgent()
}
