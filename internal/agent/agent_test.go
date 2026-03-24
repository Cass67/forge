package agent

import (
	"bytes"
	"context"
	"fmt"
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
		strings.Repeat("x", 300),
	})
	if !strings.Contains(got, "Tool results:") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "\nline2") {
		t.Fatalf("expected multiline result to be flattened: %q", got)
	}
	if len(got) > 600 {
		t.Fatalf("compact results too large: %d", len(got))
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

	if err := a.Run(context.Background(), "describe this repo"); err != nil {
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
	if driver.callIdx != 3 {
		t.Fatalf("expected dispatch retry plus delegate flow, got %d driver calls", driver.callIdx)
	}
}

func TestDispatchRejectsRepeatedScoutDelegation(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"describe repo\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"look for problems\"}}\n</tool_call>",
		"Done.",
	}}
	reg := tools.NewRegistry()
	delegateCalls := 0
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			delegateCalls++
			return "scout found stuff", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "describe this repo and suggest improvements"); err != nil {
		t.Fatal(err)
	}
	if delegateCalls != 1 {
		t.Fatalf("expected one executed scout delegation, got %d", delegateCalls)
	}
	if strings.Contains(output.String(), "look for problems") {
		t.Fatalf("unexpected repeated scout delegation rendered: %q", output.String())
	}
}

func TestDispatchRejectsScoutArchitectScoutLoop(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"describe repo\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"organize findings\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"look for more problems\"}}\n</tool_call>",
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
			return role + " result", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review repo and suggest improvements"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if strings.Contains(output.String(), "look for more problems") {
		t.Fatalf("unexpected repeated scout loop rendered: %q", output.String())
	}
}

func TestDispatchRejectsScoutRecommendationTaskAndRoutesThroughArchitect(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Inspect the repository and identify practical improvement opportunities. OUTCOME: Recommended improvements.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Inspect the repository and collect factual findings about code structure, testing, docs, and maintainability. OUTCOME: Evidence-only findings with file references. MUST NOT: Recommend changes or prioritize work.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Turn the scout findings into prioritized recommendations for the repo owner. OUTCOME: A concise prioritized improvement plan grounded in the scout evidence.\"}}\n</tool_call>",
		"Done.",
	}}
	reg := tools.NewRegistry()
	var delegated []string
	var tasks []string
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			task, _ := args["task"].(string)
			delegated = append(delegated, role)
			tasks = append(tasks, task)
			if role == "scout" {
				return "FINDINGS:\n- README is thin\nKEY FILES: /repo/README.md\nFOLLOW-UP: architect", nil
			}
			return "GOAL: prioritize repo improvements", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review this repo and suggest improvements"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if len(tasks) != 2 {
		t.Fatalf("executed tasks = %d, want 2", len(tasks))
	}
	if strings.Contains(strings.ToLower(tasks[0]), "improvement opportunit") {
		t.Fatalf("scout task should be evidence-only, got %q", tasks[0])
	}
	if !strings.Contains(strings.ToLower(tasks[1]), "prioritized recommendations") {
		t.Fatalf("architect task should synthesize recommendations, got %q", tasks[1])
	}
	if strings.Contains(output.String(), "identify practical improvement opportunities") {
		t.Fatalf("illegal scout recommendation task should not be rendered: %q", output.String())
	}
}

func TestDispatchInjectsScoutFindingsIntoArchitectTask(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Turn the scout findings into prioritized recommendations. OUTCOME: Prioritized recommendations.\"}}\n</tool_call>",
		"Done.",
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
				return "FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests\nFOLLOW-UP: architect", nil
			}
			architectTask = task
			return "GOAL: prioritize improvements", nil
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

func TestDispatchPersistsScoutRepoReviewEvidenceToScratchpad(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence for a repository review only. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Synthesize the repo review into prioritized recommendations. OUTCOME: Final review.\"}}\n</tool_call>",
	}}
	reg := tools.NewRegistry()
	type scratchWrite struct {
		topic   string
		content string
	}
	var writes []scratchWrite
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			if role == "scout" {
				return "FINDINGS:\n- docs are thin\nKEY FILES: /repo/README.md\nFOLLOW-UP: architect", nil
			}
			return "Prioritized recommendations", nil
		},
	})
	reg.Register(tools.Tool{
		Name:        "scratchpad_write",
		Description: "Write scratchpad",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			topic, _ := args["topic"].(string)
			content, _ := args["content"].(string)
			writes = append(writes, scratchWrite{topic: topic, content: content})
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
	if len(writes) != 2 {
		t.Fatalf("scratchpad writes = %d, want 2", len(writes))
	}
	if writes[0].topic != "repo_review_evidence" {
		t.Fatalf("first scratchpad topic = %q, want repo_review_evidence", writes[0].topic)
	}
	if !strings.Contains(writes[0].content, "FINDINGS:\n- docs are thin") {
		t.Fatalf("evidence scratchpad content missing scout findings: %q", writes[0].content)
	}
	if writes[1].topic != "repo_review_recommendations" {
		t.Fatalf("second scratchpad topic = %q, want repo_review_recommendations", writes[1].topic)
	}
	if !strings.Contains(writes[1].content, "Prioritized recommendations") {
		t.Fatalf("recommendation scratchpad content missing architect result: %q", writes[1].content)
	}
}

func TestDispatchAllowsArchitectRetryAfterBlockedResultWhenScratchpadContextArrives(t *testing.T) {
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
			secondArchitectTask = task
			return "GOAL: prioritized recommendations", nil
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
	if architectCalls != 1 {
		t.Fatalf("expected architect to run once after scratchpad evidence arrives, got %d calls", architectCalls)
	}
	if !strings.Contains(secondArchitectTask, "FINDINGS:\n- docs are thin") {
		t.Fatalf("retry architect task missing scratchpad findings: %q", secondArchitectTask)
	}
	if !strings.Contains(output.String(), "delegate to architect for repo-review synthesis requires a successful scout evidence pass") {
		t.Fatalf("expected initial architect attempt to be blocked before evidence, got %q", output.String())
	}
}

func TestDispatchAllowsBuilderRetryAfterBlockedResultWithSmartQuotes(t *testing.T) {
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
				return "I don’t have access to the earlier tool output in this current context, so I can’t reliably reconstruct the exact markdown content without inventing or altering it.", nil
			}
			return "file written", nil
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
		t.Fatalf("expected builder to be retried after blocked smart-quote result, got %d calls", builderCalls)
	}
}

func TestDispatchCarriesPriorArchitectResultIntoLaterBuilderTurn(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Write a remediation plan. OUTCOME: markdown plan.\"}}\n</tool_call>",
		"Done.",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"builder\", \"task\": \"TASK: Create improvments_cleanup.md with the previously recovered remediation plan text. OUTCOME: file exists.\"}}\n</tool_call>",
		"Done.",
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
				return "# Repository Cleanup and Contribution Readiness Plan\n\nExact remediation content.", nil
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
	if err := a.Run(context.Background(), "pop that in an improvments_cleanup.md file"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(builderTask, "# Repository Cleanup and Contribution Readiness Plan") {
		t.Fatalf("builder task missing prior architect result: %q", builderTask)
	}
}

func TestDispatchExecutesOnlyFirstToolCallPerTurn(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"find evidence\"}}\n</tool_call>\n<tool_call>\n{\"name\": \"scratchpad_write\", \"args\": {\"topic\": \"ignored\", \"content\": \"invented\"}}\n</tool_call>\n<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"should not run yet\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"now synthesize\"}}\n</tool_call>",
		"Done.",
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
				return "FINDINGS:\n- evidence", nil
			}
			return "GOAL: synthesize", nil
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
		t.Fatalf("unexpected dispatch scratchpad write execution count = %d", scratchWrites)
	}
}

func TestDispatchRejectsSynthesizedScratchpadWriteContent(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"scratchpad_write\", \"args\": {\"topic\": \"repo-review-scout-findings\", \"content\": \"Scout findings to carry into recommendation synthesis:\\n- invented summary\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Synthesize recommendations.\"}}\n</tool_call>",
		"Done.",
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
				return "FINDINGS:\n- real evidence\nKEY FILES: /repo/README.md", nil
			}
			architectTask = task
			return "GOAL: prioritized recommendations", nil
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
	if scratchWrites != 0 {
		t.Fatalf("dispatch should not write synthesized scratchpad content, got %d writes", scratchWrites)
	}
	if !strings.Contains(architectTask, "FINDINGS:\n- real evidence") {
		t.Fatalf("architect task missing real scout evidence: %q", architectTask)
	}
	if strings.Contains(architectTask, "invented summary") {
		t.Fatalf("architect task should not include synthesized dispatch summary: %q", architectTask)
	}
}

func TestDispatchRequiresUsableScoutEvidenceBeforeArchitectRepoReview(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence only for a repo review. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Synthesize the repo review into prioritized recommendations. OUTCOME: Final review.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Narrow the repo review evidence pass to a smaller, file-backed set of findings. OUTCOME: Evidence-backed findings only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Synthesize the repo review into prioritized recommendations. OUTCOME: Final review.\"}}\n</tool_call>",
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
				return "AGENT ERROR (scout): max turns (25) exceeded\n\nPartial output:\nFINDINGS:\n- incomplete", nil
			case 2:
				return "FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests\nFOLLOW-UP: architect", nil
			default:
				return "Prioritized recommendations", nil
			}
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review repo improvements"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,scout,architect (driver calls=%d output=%q)", got, driver.callIdx, output.String())
	}
	if strings.Contains(output.String(), "delegate to architect for repo-review synthesis requires a successful scout evidence pass") == false {
		t.Fatalf("expected dispatch guard error to be surfaced, got %q", output.String())
	}
}

func TestDispatchRejectsBuilderPresentationShimAfterArchitectRepoReview(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Synthesize the repo review into prioritized recommendations. OUTCOME: Final review.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"builder\", \"task\": \"TASK: Present the architect's synthesized repository improvement recommendations as a direct user-facing response. OUTCOME: Final concise review answer.\"}}\n</tool_call>",
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
				return "FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests\nFOLLOW-UP: architect", nil
			}
			return "Priority recommendations", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review repo improvements"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(delegated, ","); got != "scout,architect" {
		t.Fatalf("delegated roles = %q, want scout,architect", got)
	}
	if strings.Contains(output.String(), "Present the architect's synthesized") {
		t.Fatalf("builder presentation shim should not be rendered: %q", output.String())
	}
}

func TestDispatchStopsAfterSuccessfulArchitectRepoReviewSynthesis(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Synthesize the repo review into prioritized recommendations. OUTCOME: Final review.\"}}\n</tool_call>",
		"Based on the repo review, the main improvements to make are: ...",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			if role == "scout" {
				return "FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests\nFOLLOW-UP: architect", nil
			}
			return "Priority recommendations", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review repo improvements"); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 2 {
		t.Fatalf("dispatch should stop after architect synthesis, got %d driver calls", driver.callIdx)
	}
	if strings.Contains(output.String(), "Based on the repo review") {
		t.Fatalf("dispatch should not emit direct final prose after architect result: %q", output.String())
	}
}

func TestDispatchStopsAfterArchitectRepoReviewRecommendationTaskWithoutSynthesizeKeyword(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"TASK: Gather evidence only.\"}}\n</tool_call>",
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Review the scout evidence in scratchpad and produce prioritized repository improvement recommendations for the user. OUTCOME: Concise, actionable list of improvements with rationale and priority, based only on repository evidence.\"}}\n</tool_call>",
		"",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			if role == "scout" {
				return "FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests\nFOLLOW-UP: architect", nil
			}
			return "Prioritized improvement recommendations", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "review repo improvements"); err != nil {
		t.Fatal(err)
	}
	if driver.callIdx != 2 {
		t.Fatalf("dispatch should stop after architect recommendation task, got %d driver calls", driver.callIdx)
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
	if err := agent.Run(context.Background(), "review repo"); err != nil {
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
		"<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"architect\", \"task\": \"TASK: Synthesize the repo review into prioritized recommendations. OUTCOME: Final review.\"}}\n</tool_call>",
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
				return "FINDINGS:\n- tests are sparse\nKEY FILES: /repo/tests\nFOLLOW-UP: architect", nil
			default:
				return "Prioritized recommendations", nil
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

func TestCancelSubAgentSafe(t *testing.T) {
	a := &Agent{}
	// Should not panic when no sub-agent is active.
	a.CancelSubAgent()
}
