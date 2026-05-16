# Forge Robustness Roadmap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the known Forge stability classes so local-model mistakes, malformed tool calls, and TUI row growth do not collapse a turn or hide core UI state.

**Architecture:** Convert robustness from an incident-by-incident theme into enforceable gates: model-visible tool contracts must be typed and placeholder-free, model-correctable failures must become tool feedback or retry prompts, and the TUI must render from one explicit height budget. Each task adds a failing test that captures a class of failures before changing production code.

**Tech Stack:** Go, `internal/react` runner/session, `internal/agent/tools` registry, `internal/react/tools`, OpenAI-compatible native tool schemas, Bubble Tea/Lipgloss TUI.

---

## Audit Baseline

Existing docs already identify the direction but do not define a single acceptance gate:
- `docs/superpowers/specs/2026-05-15-typed-tool-schema-reliability-design.md` says structured schema validation should make validation failures normal tool results, but malformed top-level args remain terminal.
- `docs/superpowers/plans/2026-05-15-tool-turn-reliability.md` handles missing required args, but not every model-correctable failure class.
- `docs/reports/2026-05-07-codex-source-review-for-forge.md` recommends protocol/lifecycle clarity, generated schemas, and explicit event shapes, but those are not yet runtime acceptance gates.

Current concrete gaps found in code:
- `internal/agent/tools/registry.go:344-367` still returns model-visible `"TODO"` for `pattern` examples.
- `internal/agent/tools/registry.go:299-330` can still produce XML example markup for hidden/textual tool help; tests must ensure native tool prompts never expose it.
- `internal/react/loop.go:1239-1244` and `internal/react/loop.go:1298-1303` treat deprecated XML markup as retryable internally but still surface a terminal turn error after retry exhaustion.
- `internal/react/loop.go:1400-1408` treats malformed top-level native tool args as terminal, even though this is a model-correctable output error.
- `internal/react/loop.go:1513-1526` only marks `ask_user_question` execution errors as recoverable; the policy is hardcoded and not classified by error kind.
- `internal/tui/chatmodel.go:973-1037` and `internal/tui/chatmodel.go:5510-5595` calculate rendered rows in separate places; new rows can be added without updating viewport height.
- `internal/tui/chatstats.go:333-335` has a height-aware header function that ignores height; the current policy is “always preserve model + dir,” but it is not tied to the layout budget.

## Completion Gates

This roadmap is complete only when all of these commands pass and the stated invariants are true:

- `go test -count=1 ./internal/agent/tools ./internal/react ./internal/react/tools ./internal/tui ./internal/llm/drivers`
- `just build`
- No model-visible tool example contains `TODO`, `options_json`, `steps_json`, or XML tool-call markup in native-tool mode.
- Missing, malformed, or semantically invalid tool args are delivered to the model as tool feedback and do not create chat `Error:` rows.
- Deprecated XML tool-call markup from a native provider does not execute a tool and does not finish as a noisy fatal UI error while retries remain possible.
- Normal chat view height is `<= m.height` and the first visible rows preserve title/model/dir across idle, busy, done, error, stats-footer, queued-input, and short-terminal states.

---

### Task 1: Remove Model-Visible Placeholder Examples

**Files:**
- Modify: `internal/agent/tools/registry.go:333-367`
- Test: `internal/agent/tools/registry_test.go`

- [ ] **Step 1: Write the failing tests**

Add these tests to `internal/agent/tools/registry_test.go`:

```go
func TestExampleValueForPatternParameterIsConcrete(t *testing.T) {
	got := exampleValueForParameter(ParameterDef{Name: "pattern", Type: "string", Required: true})
	if got == "TODO" || got == "" {
		t.Fatalf("pattern example = %q, want concrete glob-like example", got)
	}
	if got != "*.go" {
		t.Fatalf("pattern example = %q, want *.go", got)
	}
}

func TestToolExampleArgsNeverContainTODO(t *testing.T) {
	tool := Tool{
		Name: "glob",
		Parameters: []ParameterDef{{Name: "pattern", Type: "string", Required: true}},
	}
	args := toolExampleArgs(tool)
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "TODO") {
		t.Fatalf("tool example args contain TODO: %s", encoded)
	}
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `go test -count=1 ./internal/agent/tools -run 'TestExampleValueForPatternParameterIsConcrete|TestToolExampleArgsNeverContainTODO'`

Expected: FAIL because `exampleValueForParameter` returns `"TODO"` for pattern parameters.

- [ ] **Step 3: Implement the minimal fix**

In `internal/agent/tools/registry.go`, change the `pattern` case in `exampleValueForParameter`:

```go
case strings.Contains(name, "pattern"):
	return "*.go"
```

- [ ] **Step 4: Run the focused test and verify it passes**

Run: `go test -count=1 ./internal/agent/tools -run 'TestExampleValueForPatternParameterIsConcrete|TestToolExampleArgsNeverContainTODO'`

Expected: PASS.

- [ ] **Step 5: Commit the task**

Run:

```bash
git add internal/agent/tools/registry.go internal/agent/tools/registry_test.go
git commit -m "fix: remove placeholder tool examples"
```

Expected: commit succeeds. If unrelated changes exist in these files, stage only the hunks from this task.

---

### Task 2: Enforce No JSON-In-String Tool Contracts

**Files:**
- Modify: `internal/runtime/chat_test.go`
- Modify only if the test finds violations: tool files under `internal/react/tools/` and `internal/agent/tools/`

- [ ] **Step 1: Write the registry contract test**

Add this test to `internal/runtime/chat_test.go`, where the full chat registry can be built through the package-local `registerTools` helper:

```go
func TestToolContractsDoNotUseJSONInStringParameters(t *testing.T) {
	cfg := config.Default()
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, session, approve, nil, nil)

	for _, tool := range reg.All() {
		for _, param := range tool.Parameters {
			name := strings.ToLower(param.Name)
			desc := strings.ToLower(param.Description)
			if strings.Contains(name, "json") || strings.Contains(desc, "json array") || strings.Contains(desc, "json object") {
				t.Fatalf("tool %s parameter %s exposes JSON-in-string contract: %q", tool.Name, param.Name, param.Description)
			}
		}
	}
}
```

- [ ] **Step 2: Run the contract test**

Run: `go test -count=1 ./internal/runtime -run TestToolContractsDoNotUseJSONInStringParameters`

Expected: PASS after the current `update_plan` and `ask_user_question` migrations. If it fails, migrate the listed tool from `ParameterDef` JSON strings to `Tool.Schema` before continuing.

- [ ] **Step 3: Add a schema coverage test for structured tools**

Add this test to `internal/runtime/chat_test.go`:

```go
func TestStructuredToolsExposeNativeSchema(t *testing.T) {
	cfg := config.Default()
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, session, approve, nil, nil)

	for _, name := range []string{"update_plan", "ask_user_question"} {
		tool, ok := reg.Get(name)
		if !ok {
			t.Fatalf("missing tool %s", name)
		}
		if tool.Schema == nil {
			t.Fatalf("tool %s has nil schema", name)
		}
	}
}
```

- [ ] **Step 4: Run the schema coverage test**

Run: `go test -count=1 ./internal/runtime -run TestStructuredToolsExposeNativeSchema`

Expected: PASS.

- [ ] **Step 5: Commit the task**

Run:

```bash
git add internal/runtime/chat_test.go internal/react/tools
git commit -m "test: enforce native tool contract robustness"
```

Expected: commit includes only contract tests and any required schema migrations.

---

### Task 3: Make Malformed Native Tool Args Recoverable

**Files:**
- Modify: `internal/react/loop.go:1400-1408`
- Test: `internal/react/loop_test.go:1900-1947`

- [ ] **Step 1: Replace the existing terminal malformed-args test with a recovery test**

In `internal/react/loop_test.go`, update the malformed args test so the scripted driver first emits malformed tool args, then emits a final recovery answer:

```go
func TestRunnerNativePathHandlesMalformedArgsJSONAsToolFeedback(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "bad-json", Name: "read_file", ArgsJSON: `{"path":`}}},
		{{Text: "recovered after malformed args feedback"}},
	}}
	reg := agenttools.NewRegistry()
	executed := false
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			executed = true
			return "should not execute", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "read README"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if executed {
		t.Fatal("tool executed despite malformed JSON args")
	}
	if got := r.LastResponse(); got != "recovered after malformed args feedback" {
		t.Fatalf("LastResponse = %q", got)
	}
	found := false
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "bad-json" && strings.Contains(msg.Content, "malformed tool call arguments") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing malformed-args feedback: %#v", session.Snapshot().History)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test -count=1 ./internal/react -run TestRunnerNativePathHandlesMalformedArgsJSONAsToolFeedback`

Expected: FAIL because the runner currently returns the malformed args error.

- [ ] **Step 3: Convert malformed args into tool feedback**

In `internal/react/loop.go`, replace the terminal parse-error branch with a recoverable branch:

```go
if err := json.Unmarshal([]byte(call.ArgsJSON), &args); err != nil {
	parseErr := fmt.Sprintf("error: malformed tool call arguments for %q: %v", call.Name, err)
	if r.renderer != nil {
		r.renderer.ToolCall(call.Name, "malformed arguments")
		r.renderer.ToolResult(call.Name, parseErr, "", true)
	}
	r.session.AppendNativeToolResult(call.ID, parseErr)
	r.updatePlanWorkflow(call.Name, nil, "", true)
	r.updateSameFileSearchWorkflow(call.Name, nil, true)
	continue
}
```

Do not execute hooks or the tool when args cannot be parsed.

- [ ] **Step 4: Run the focused test and nearby regressions**

Run: `go test -count=1 ./internal/react -run 'TestRunnerNativePathHandlesMalformedArgsJSONAsToolFeedback|TestRunnerToolValidationFailureContinuesLoop|TestRunnerUpdatePlanMissingStepsContinuesLoop'`

Expected: PASS.

- [ ] **Step 5: Commit the task**

Run:

```bash
git add internal/react/loop.go internal/react/loop_test.go
git commit -m "fix: recover from malformed native tool args"
```

Expected: commit succeeds with only malformed-args recovery changes.

---

### Task 4: Classify Recoverable Tool Execution Errors Centrally

**Files:**
- Modify: `internal/react/loop.go:1513-1526`
- Test: `internal/react/loop_test.go`

- [ ] **Step 1: Add tests for recoverable and fatal execution errors**

Add these tests near `TestRunnerAskUserQuestionExecutionErrorContinuesLoop` in `internal/react/loop_test.go`:

```go
func TestRunnerRecoverableToolExecutionErrorContinuesLoop(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "ask-1", Name: "ask_user_question", ArgsJSON: `{"question":"Pick one","options":[{"label":"Only one"}]}`}}},
		{{Text: "recovered after ask feedback"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "ask_user_question",
		Description: "ask user",
		Schema:      askUserQuestionTestSchema(),
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "", fmt.Errorf("at least two options are required")
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "ask", Operation: "plan"})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "ask"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := r.LastResponse(); got != "recovered after ask feedback" {
		t.Fatalf("LastResponse = %q", got)
	}
}

func TestRunnerFatalToolExecutionErrorStopsLoop(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"README.md","content":"x"}`}}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		Parameters: []agenttools.ParameterDef{
			{Name: "path", Type: "string", Required: true},
			{Name: "content", Type: "string", Required: true},
		},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "", fmt.Errorf("disk unavailable")
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: NewSession()})

	if err := r.Run(context.Background(), "write file"); err == nil || !strings.Contains(err.Error(), "disk unavailable") {
		t.Fatalf("err = %v, want fatal disk error", err)
	}
}
```

Add helper:

```go
func askUserQuestionTestSchema() *llm.ToolSchema {
	additional := false
	return &llm.ToolSchema{
		Type: "object",
		Properties: map[string]*llm.ToolSchema{
			"question": {Type: "string"},
			"options": {
				Type: "array",
				Items: &llm.ToolSchema{
					Type:                 "object",
					Properties:           map[string]*llm.ToolSchema{"label": {Type: "string"}},
					Required:             []string{"label"},
					AdditionalProperties: &additional,
				},
			},
		},
		Required:             []string{"question", "options"},
		AdditionalProperties: &additional,
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `go test -count=1 ./internal/react -run 'TestRunnerRecoverableToolExecutionErrorContinuesLoop|TestRunnerFatalToolExecutionErrorStopsLoop'`

Expected: PASS if current `ask_user_question` special case is retained and fatal write errors still stop the loop.

- [ ] **Step 3: Replace the hardcoded function with explicit classification**

Rename `isRecoverableToolExecutionError` to `isModelCorrectableToolExecutionError` and make the policy comment explicit:

```go
func isModelCorrectableToolExecutionError(name string, err error) bool {
	if err == nil {
		return false
	}
	switch name {
	case "ask_user_question":
		return true
	default:
		return false
	}
}
```

Call it as:

```go
if isModelCorrectableToolExecutionError(call.Name, res.err) {
	continue
}
```

- [ ] **Step 4: Run the tests again**

Run: `go test -count=1 ./internal/react -run 'TestRunnerRecoverableToolExecutionErrorContinuesLoop|TestRunnerFatalToolExecutionErrorStopsLoop|TestRunnerAskUserQuestionExecutionErrorContinuesLoop'`

Expected: PASS.

- [ ] **Step 5: Commit the task**

Run:

```bash
git add internal/react/loop.go internal/react/loop_test.go
git commit -m "test: classify recoverable tool execution errors"
```

Expected: commit succeeds with tests documenting the boundary.

---

### Task 5: Make Deprecated XML Markup Recover Without Noisy UI Errors

**Files:**
- Modify: `internal/react/loop.go:1239-1244`, `internal/react/loop.go:1298-1303`, `internal/react/loop.go:680-701`
- Test: `internal/react/loop_test.go:1750-1836`
- Test: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Update XML retry tests to expect recovery when the model corrects itself**

Replace the terminal expectation in `TestRunnerRejectsLegacyXMLToolCallMarkupFromNativeProvider` with this behavior:

```go
func TestRunnerRecoversFromLegacyXMLToolCallMarkupFromNativeProvider(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{
		"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
		"recovered with native response",
	}}
	reg := agenttools.NewRegistry()
	called := false
	reg.Register(agenttools.Tool{
		Name:        "list_dir",
		Description: "list a directory",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "README.md\ninternal/", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: NewSession()})

	if err := r.Run(context.Background(), "whats this repo all about"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if called {
		t.Fatal("legacy XML markup should not execute a tool")
	}
	if got := r.LastResponse(); got != "recovered with native response" {
		t.Fatalf("LastResponse = %q", got)
	}
}
```

- [ ] **Step 2: Add a retry-exhaustion test that does not render duplicate chat errors**

In `internal/tui/chatmodel_test.go`, add:

```go
func TestRetryableXMLMarkupErrorDoesNotCreateDuplicateChatError(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "localllm/gpt-oss-20b", WorkDir: "/tmp"})
	m.debugEnabled = false

	updated, _ := m.Update(llm.Event{Kind: llm.EventError, Text: "react runtime: provider returned deprecated XML tool-call markup"})
	m = updated.(ChatModel)

	count := 0
	for _, msg := range m.messages {
		if msg.Kind == MsgStatus && strings.Contains(msg.Content, "deprecated XML tool-call markup") {
			count++
		}
	}
	if count > 1 {
		t.Fatalf("duplicate XML markup errors = %d, messages = %#v", count, m.messages)
	}
}
```

- [ ] **Step 3: Run tests and capture current behavior**

Run: `go test -count=1 ./internal/react ./internal/tui -run 'TestRunnerRecoversFromLegacyXMLToolCallMarkupFromNativeProvider|TestRetryableXMLMarkupErrorDoesNotCreateDuplicateChatError'`

Expected: The recovery test should pass if retry behavior is already correct for one correction; the TUI test may fail if duplicate error rows are still emitted.

- [ ] **Step 4: Add an XML-specific final failure message**

If retry exhaustion still creates a fatal error, keep the error fatal after `maxCompletionRetriesPerTurn`, but make it concise and non-duplicated by ensuring the runtime emits retry notices during retries and only one final `EventError` at exhaustion. Do not parse or execute XML markup from native providers.

- [ ] **Step 5: Suppress duplicate TUI status for retryable XML markup**

In `internal/tui/chatmodel.go`, add a helper near `eventErrorMessage` or the event error branch:

```go
func isModelOutputFormatError(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, "deprecated xml tool-call markup")
}
```

In the `llm.EventError` case, if `isModelOutputFormatError(errMsg)` is true, add at most one status row and avoid adding both `Error: ...` and progress-slot `· error: ...` copies.

- [ ] **Step 6: Run XML tests**

Run: `go test -count=1 ./internal/react ./internal/tui -run 'XML|Deprecated|RetryableXML|LegacyXML'`

Expected: PASS. XML markup is never executed as a tool; correction retries can recover; exhaustion is represented once.

- [ ] **Step 7: Commit the task**

Run:

```bash
git add internal/react/loop.go internal/react/loop_test.go internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git commit -m "fix: recover cleanly from XML tool markup"
```

Expected: commit succeeds with XML recovery behavior only.

---

### Task 6: Centralize TUI Normal Layout Budget

**Files:**
- Modify: `internal/tui/chatmodel.go:973-1037`, `internal/tui/chatmodel.go:5510-5595`
- Test: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Add a failing layout-budget unit test**

Add this test to `internal/tui/chatmodel_test.go`:

```go
func TestChatModelNormalLayoutBudgetMatchesRenderedRows(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "localllm/gpt-oss-20b", WorkDir: "/Users/cass/git/forge"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updated.(ChatModel)
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "test"})
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:01", Content: strings.Repeat("response ", 30)})
	updated, _ = m.Update(llm.Event{Kind: llm.EventStats, Duration: time.Second, Usage: llm.Usage{InputTokens: 1200, OutputTokens: 300}})
	m = updated.(ChatModel)
	updated, _ = m.Update(llm.Event{Kind: llm.EventDone})
	m = updated.(ChatModel)

	view := strippedLine(m.View())
	lines := strings.Split(view, "\n")
	if len(lines) > m.height {
		t.Fatalf("view rows = %d, want <= %d\n%s", len(lines), m.height, view)
	}
	for i, line := range lines {
		if width := ansiPrintableWidth(line); width > m.width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, width, m.width, line)
		}
	}
	if !strings.Contains(lines[0], "localllm/gpt-oss-20b") || !strings.Contains(lines[1], "model") || !strings.Contains(lines[2], "dir") {
		t.Fatalf("header not preserved at top:\n%s", strings.Join(lines[:min(len(lines), 4)], "\n"))
	}
}
```

- [ ] **Step 2: Run the layout test**

Run: `go test -count=1 ./internal/tui -run TestChatModelNormalLayoutBudgetMatchesRenderedRows`

Expected: PASS after the current `liveStatusSlotHeight` fix. If it fails, continue with Step 3.

- [ ] **Step 3: Introduce a single layout budget struct**

In `internal/tui/chatmodel.go`, add near `chatLayoutMouseContext`:

```go
type normalChatLayoutBudget struct {
	Header      int
	HeaderGap   int
	Chat        int
	DebugDock   int
	Pending     int
	LiveStatus  int
	Input       int
	StatsFooter int
	Total       int
}

func (m ChatModel) normalChatLayoutBudget() normalChatLayoutBudget {
	b := normalChatLayoutBudget{
		Header:      m.headerHeight(),
		HeaderGap:   chatHeaderGapHeight,
		DebugDock:   m.debugDockHeight(),
		Pending:     m.pendingInputPreviewHeight(),
		LiveStatus:  m.liveStatusSlotHeight(),
		Input:       m.inputHeight(),
		StatsFooter: m.normalModeStatsFooterHeight(),
	}
	b.Chat = max(1, m.height-b.Header-b.HeaderGap-b.DebugDock-b.Pending-b.LiveStatus-b.Input-b.StatsFooter)
	b.Total = b.Header + b.HeaderGap + b.Chat + b.DebugDock + b.Pending + b.LiveStatus + b.Input + b.StatsFooter
	return b
}
```

Add:

```go
func (m ChatModel) pendingInputPreviewHeight() int {
	if len(m.pendingQueuedInput) == 0 {
		return 0
	}
	lines := 1 + min(3, len(m.pendingQueuedInput))
	if len(m.pendingQueuedInput) > 3 {
		lines++
	}
	return lines + 2
}
```

- [ ] **Step 4: Use the budget in resize and render**

In `resizeChatViewport`, replace the ad hoc `bodyH` calculation with:

```go
budget := m.normalChatLayoutBudget()
bodyH := budget.Chat
```

In `View`, compute `budget := m.normalChatLayoutBudget()` once and use `budget.Chat` for `chatBodyHeight`. Keep `renderPendingInputPreview` as the renderer, but the budget must reserve its height.

- [ ] **Step 5: Add queued-input layout coverage**

Add this test:

```go
func TestChatModelNormalLayoutBudgetReservesQueuedInputPreview(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "localllm/gpt", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 14})
	m = updated.(ChatModel)
	m.pendingQueuedInput = []string{"first queued", "second queued", "third queued", "fourth queued"}
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:01", Content: strings.Repeat("response ", 20)})
	m.resizeChatViewport()

	view := strippedLine(m.View())
	lines := strings.Split(view, "\n")
	if len(lines) > m.height {
		t.Fatalf("view rows = %d, want <= %d\n%s", len(lines), m.height, view)
	}
	if !strings.Contains(lines[0], "FORGE") || !strings.Contains(lines[1], "model") || !strings.Contains(lines[2], "dir") {
		t.Fatalf("header not preserved with queued preview:\n%s", strings.Join(lines[:min(len(lines), 4)], "\n"))
	}
}
```

- [ ] **Step 6: Run TUI layout tests**

Run: `go test -count=1 ./internal/tui -run 'LayoutBudget|StatsFooter|QueuedInput|HeaderAtTop'`

Expected: PASS.

- [ ] **Step 7: Commit the task**

Run:

```bash
git add internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git commit -m "fix: centralize chat layout budget"
```

Expected: commit succeeds. If unrelated local-provider changes exist in `chatmodel.go`, stage only the layout-budget hunks.

---

### Task 7: Add A Normal View Invariant Test Matrix

**Files:**
- Modify: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Add the invariant helper**

Add this helper to `internal/tui/chatmodel_test.go`:

```go
func assertNormalViewFitsAndKeepsHeader(t *testing.T, m ChatModel) {
	t.Helper()
	view := strippedLine(m.View())
	lines := strings.Split(view, "\n")
	if len(lines) > m.height {
		t.Fatalf("view rows = %d, want <= %d\n%s", len(lines), m.height, view)
	}
	for i, line := range lines {
		if width := ansiPrintableWidth(line); width > m.width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, width, m.width, line)
		}
	}
	if len(lines) >= 3 {
		if !strings.Contains(lines[0], "localllm/gpt") && !strings.Contains(lines[0], "FORGE") {
			t.Fatalf("first row missing title/model:\n%s", strings.Join(lines[:min(len(lines), 4)], "\n"))
		}
		if !strings.Contains(lines[1], "model") {
			t.Fatalf("second row missing model line:\n%s", strings.Join(lines[:min(len(lines), 4)], "\n"))
		}
		if !strings.Contains(lines[2], "dir") {
			t.Fatalf("third row missing dir line:\n%s", strings.Join(lines[:min(len(lines), 4)], "\n"))
		}
	}
}
```

- [ ] **Step 2: Add the state matrix test**

Add this test:

```go
func TestChatModelNormalViewInvariantMatrix(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(ChatModel) ChatModel
	}{
		{name: "idle", setup: func(m ChatModel) ChatModel { return m }},
		{name: "busy", setup: func(m ChatModel) ChatModel { m.busy = true; m.status = "working"; return m }},
		{name: "done_with_stats", setup: func(m ChatModel) ChatModel {
			m.statsUsage = llm.Usage{InputTokens: 100, OutputTokens: 20}
			m.sessionUsage = llm.Usage{InputTokens: 100, OutputTokens: 20}
			m.syncStatusData()
			m.resizeChatViewport()
			return m
		}},
		{name: "error_status", setup: func(m ChatModel) ChatModel { m.status = "error"; m.flash = "error: failed"; return m }},
		{name: "queued_input", setup: func(m ChatModel) ChatModel { m.pendingQueuedInput = []string{"queued one", "queued two"}; m.resizeChatViewport(); return m }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewChatModel(ChatLiveConfig{Model: "localllm/gpt", WorkDir: "/tmp/forge"})
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 14})
			m = updated.(ChatModel)
			m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:01", Content: strings.Repeat("response ", 20)})
			m = tc.setup(m)
			assertNormalViewFitsAndKeepsHeader(t, m)
		})
	}
}
```

- [ ] **Step 3: Run the matrix**

Run: `go test -count=1 ./internal/tui -run TestChatModelNormalViewInvariantMatrix`

Expected: PASS after Task 6. If it fails, fix `normalChatLayoutBudget` rather than patching individual render branches.

- [ ] **Step 4: Commit the task**

Run:

```bash
git add internal/tui/chatmodel_test.go
git commit -m "test: lock chat view layout invariants"
```

Expected: commit succeeds with tests only.

---

### Task 8: Add Robustness Audit Command Tests

**Files:**
- Modify: `internal/runtime/chat_test.go`
- Modify: `internal/react/loop_test.go`
- Modify: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Add a tool-contract audit test**

Add a test that fails if any registered model-visible tool contract contains known-bad strings:

```go
func TestModelVisibleToolContractsDoNotContainKnownBadPlaceholders(t *testing.T) {
	cfg := config.Default()
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, session, approve, nil, nil)

	bad := []string{"TODO", "steps_json", "options_json", "<tool_call>"}
	for _, tool := range reg.All() {
		blob, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		text := string(blob)
		for _, needle := range bad {
			if strings.Contains(text, needle) {
				t.Fatalf("tool %s contract contains %q: %s", tool.Name, needle, text)
			}
		}
	}
}
```

- [ ] **Step 2: Add focused runtime robustness grep test**

Do not shell out from Go. Instead, add/keep explicit tests for these runtime behaviors:

```go
// Covered by tests:
// - TestRunnerNativePathHandlesMalformedArgsJSONAsToolFeedback
// - TestRunnerToolValidationFailureContinuesLoop
// - TestRunnerUpdatePlanMissingStepsContinuesLoop
// - TestRunnerRecoverableToolExecutionErrorContinuesLoop
// - TestRunnerRecoversFromLegacyXMLToolCallMarkupFromNativeProvider
```

If any listed test does not exist after prior tasks, add it before proceeding.

- [ ] **Step 3: Add focused TUI robustness coverage list**

Ensure these tests exist after prior tasks:

```go
// Covered by tests:
// - TestChatModelNormalLayoutBudgetMatchesRenderedRows
// - TestChatModelNormalLayoutBudgetReservesQueuedInputPreview
// - TestChatModelNormalViewInvariantMatrix
// - TestRecoverableToolValidationResultDoesNotCreateChatError
// - TestAskUserQuestionRecoverableResultDoesNotCreateChatError
```

If any listed test does not exist, add it before proceeding.

- [ ] **Step 4: Run the full robustness suite**

Run: `go test -count=1 ./internal/agent/tools ./internal/react ./internal/react/tools ./internal/tui ./internal/llm/drivers`

Expected: PASS.

- [ ] **Step 5: Build**

Run: `just build`

Expected: `go build ... -o ./bin/forge ./cmd/forge` exits 0.

- [ ] **Step 6: Commit the final gates**

Run:

```bash
git add internal/agent/tools internal/react internal/tui internal/llm/drivers
git commit -m "test: add robustness acceptance gates"
```

Expected: commit succeeds with final acceptance tests and only necessary fixes.

---

## Execution Notes

- Keep commits focused. Do not stage unrelated local-provider wizard changes currently present in `internal/tui/chatmodel.go` unless the task explicitly touches those hunks.
- If a file has unrelated dirty changes, use partial staging and inspect `git diff --cached` before every commit.
- Do not reintroduce compatibility aliases like `steps_json` or `options_json`.
- Do not parse XML tool markup from native providers as a valid tool call. Recovery means retrying or reporting controlled feedback, not executing deprecated markup.
- Prefer central budget/policy helpers over branch-local fixes.

## Self-Review

- Spec coverage: The plan covers placeholder examples, JSON-in-string contracts, malformed tool args, semantic tool execution errors, XML markup recovery, TUI layout budgeting, and final acceptance gates.
- Placeholder scan: No `TODO`, `TBD`, or “implement later” placeholders remain in the plan instructions. Mentions of `TODO` are concrete bad strings to eliminate from code.
- Type consistency: The plan uses existing `agenttools.Tool`, `ParameterDef`, `llm.ToolSchema`, `ChatModel`, `llm.Event`, and test helpers already present in the repository.
