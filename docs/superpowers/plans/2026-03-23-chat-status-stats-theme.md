# Chat Status, Stats, and Theme Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade the Bubble Tea chat UI with a provider-aware two-line status header, a comprehensive `/stats` overlay, and a chat-only named theme system.

**Architecture:** Keep the work chat-local and centered on `ChatModel`, but split rendering/data helpers into small focused files so the main model does not absorb every concern. Themes become a named palette registry, header summaries become provider-aware formatters keyed off the active model, and `/stats` becomes a sectioned diagnostics sheet built from the best available chat/runtime/provider data.

**Tech Stack:** Go, Bubble Tea, Lipgloss, existing `internal/copilot`, `internal/codexusage`, `internal/modelcatalog`, `internal/llm` usage/reporting hooks.

---

## File Map

### Existing files to modify

- Modify: `/Users/cass/git/forge/internal/tui/chatmodel.go`
  Purpose: keep state transitions, slash commands, and top-level chat `View()` orchestration.
- Modify: `/Users/cass/git/forge/internal/tui/chatmodel_test.go`
  Purpose: regression and view-level coverage for header, `/stats`, provider-aware behavior, and themes.
- Modify: `/Users/cass/git/forge/internal/tui/chatshared.go`
  Purpose: extend shared chat config/session snapshot types if new stats/theme state must persist.
- Modify: `/Users/cass/git/forge/internal/runtime/chat.go`
  Purpose: plumb best-available provider/model/runtime diagnostics into `ChatLiveConfig`.
- Modify: `/Users/cass/git/forge/internal/llm/types.go`
  Purpose: only if existing usage/state structs need a small extension for chat diagnostics.

### New files to create

- Create: `/Users/cass/git/forge/internal/tui/chattheme.go`
  Purpose: define the chat-only theme registry, aliases, and palette lookup helpers.
- Create: `/Users/cass/git/forge/internal/tui/chatstats.go`
  Purpose: assemble provider-aware header summaries and `/stats` section rows from chat/runtime data.
- Create: `/Users/cass/git/forge/internal/tui/chattheme_test.go`
  Purpose: isolate theme selection and alias/cycling tests.
- Create: `/Users/cass/git/forge/internal/tui/chatstats_test.go`
  Purpose: isolate provider-aware summary and `/stats` section rendering tests.

### Existing data sources to reuse

- `/Users/cass/git/forge/internal/copilot/quota.go`
- `/Users/cass/git/forge/internal/copilot/user_quota.go`
- `/Users/cass/git/forge/internal/codexusage/usage.go`
- `/Users/cass/git/forge/internal/modelcatalog/catalog.go`
- `/Users/cass/git/forge/internal/llm/types.go`

Do not create new provider backends for this work. Reuse the existing data paths and degrade gracefully when a field is unavailable.

---

### Task 1: Introduce a Chat-Only Named Theme Registry

**Files:**
- Create: `/Users/cass/git/forge/internal/tui/chattheme.go`
- Create: `/Users/cass/git/forge/internal/tui/chattheme_test.go`
- Modify: `/Users/cass/git/forge/internal/tui/chatmodel.go`
- Test: `/Users/cass/git/forge/internal/tui/chattheme_test.go`
- Test: `/Users/cass/git/forge/internal/tui/chatmodel_test.go`

- [ ] **Step 1: Write the failing theme registry tests**

```go
func TestChatThemeLookupSupportsNamedThemes(t *testing.T) {
	names := []string{"default", "low", "light", "dusk"}
	for _, name := range names {
		if _, ok := lookupChatTheme(name); !ok {
			t.Fatalf("missing theme %q", name)
		}
	}
}

func TestChatThemeLookupSupportsLegacyAliases(t *testing.T) {
	theme, ok := lookupChatTheme("default")
	if !ok {
		t.Fatal("default theme missing")
	}
	alias, ok := lookupChatTheme("low")
	if !ok || alias.ID != "low" {
		t.Fatalf("low alias = %#v, ok=%v", alias, ok)
	}
	if theme.ID != "default" {
		t.Fatalf("default theme = %#v", theme)
	}
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test ./internal/tui/... -run 'TestChatTheme'`
Expected: FAIL because the theme registry does not exist yet

- [ ] **Step 3: Add `chattheme.go` with a named registry**

```go
type chatTheme struct {
	ID              string
	Label           string
	AppBG           lipgloss.Color
	PanelBG         lipgloss.Color
	HeaderBG        lipgloss.Color
	HeaderFG        lipgloss.Color
	Border          lipgloss.Color
	BorderFocus     lipgloss.Color
	Text            lipgloss.Color
	TextDim         lipgloss.Color
	AccentPrimary   lipgloss.Color
	AccentSecondary lipgloss.Color
	Success         lipgloss.Color
	Warning         lipgloss.Color
	Error           lipgloss.Color
}

func lookupChatTheme(name string) (chatTheme, bool) { /* alias + registry lookup */ }
func orderedChatThemes() []chatTheme { /* stable cycle order */ }
```

- [ ] **Step 4: Replace `lowContrast` state with a theme ID in `ChatModel`**

Implement the minimum model changes:

```go
type ChatModel struct {
	themeID string
}

func (m ChatModel) theme() chatTheme {
	theme, ok := lookupChatTheme(m.themeID)
	if !ok {
		theme, _ = lookupChatTheme("default")
	}
	return theme
}
```

- [ ] **Step 5: Update `/theme`, `/theme low`, and `/theme default` behavior**

Keep command compatibility while adding named themes:

- `/theme` cycles through `orderedChatThemes()`
- `/theme <name>` picks a named theme
- invalid names flash a clear error

- [ ] **Step 6: Add view-level tests for theme selection**

```go
func TestChatModelSlashThemeSelectsNamedTheme(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.inputBuf = "/theme light"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)
	if m.themeID != "light" {
		t.Fatalf("themeID = %q", m.themeID)
	}
}
```

- [ ] **Step 7: Run the theme test subset and make it green**

Run: `go test ./internal/tui/... -run 'TestChatTheme|TestChatModelSlashTheme'`
Expected: PASS

- [ ] **Step 8: Commit the theme foundation**

```bash
git add internal/tui/chattheme.go internal/tui/chattheme_test.go internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git commit -m "feat: add chat-only named theme registry"
```

---

### Task 2: Add Provider-Aware Chat Status Data Helpers

**Files:**
- Create: `/Users/cass/git/forge/internal/tui/chatstats.go`
- Create: `/Users/cass/git/forge/internal/tui/chatstats_test.go`
- Modify: `/Users/cass/git/forge/internal/runtime/chat.go`
- Modify: `/Users/cass/git/forge/internal/tui/chatmodel.go`
- Modify: `/Users/cass/git/forge/internal/tui/chatshared.go`
- Test: `/Users/cass/git/forge/internal/tui/chatstats_test.go`

- [ ] **Step 1: Write failing tests for provider-aware summary formatting**

```go
func TestChatStatusSummaryCopilotModelUsesCopilotQuota(t *testing.T) {
	summary := buildProviderStatusSummary(chatStatusData{
		Model: "copilot/gpt-5",
		CopilotLive: &copilot.UserQuota{
			Windows: map[string]llm.CopilotQuota{
				"premium": {Type: "premium_interactions", Remaining: 143},
			},
		},
	})
	if !strings.Contains(summary, "143") {
		t.Fatalf("summary = %q", summary)
	}
}

func TestChatStatusSummaryOtherProviderSkipsSubscriptionText(t *testing.T) {
	summary := buildProviderStatusSummary(chatStatusData{Model: "anthropic/claude-sonnet-4-6"})
	if strings.Contains(strings.ToLower(summary), "copilot") || strings.Contains(strings.ToLower(summary), "codex") {
		t.Fatalf("summary = %q", summary)
	}
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test ./internal/tui/... -run 'TestChatStatusSummary'`
Expected: FAIL because status helpers do not exist yet

- [ ] **Step 3: Define a focused `chatStatusData` helper type**

`chatstats.go` should gather only what the formatter needs, for example:

```go
type chatStatusData struct {
	Model            string
	ThemeID          string
	Status           string
	LastUsage        llm.Usage
	SessionUsage     llm.Usage
	RequestMode      string
	ContextUsed      int
	ContextLimit     int
	CopilotLive      *copilot.UserQuota
	CodexUsage       *codexusage.Snapshot
	ModelInfo        *modelcatalog.ModelInfo
}
```

- [ ] **Step 4: Implement provider-aware summary builders**

Implement small, testable helpers:

- `buildStatusLine1(data chatStatusData) string`
- `buildStatusLine2(data chatStatusData) string`
- `buildProviderStatusSummary(data chatStatusData) string`
- `buildContextSummary(data chatStatusData) string`

Keep them string-focused so they are easy to test independent of Lipgloss.

- [ ] **Step 5: Extend `ChatLiveConfig` only as needed**

Add narrow best-available callbacks or snapshots instead of pushing unrelated runtime state into the model:

```go
type ChatLiveConfig struct {
	LiveCopilotQuota func() *copilot.UserQuota
	CodexUsage       func() *codexusage.Snapshot
	ModelInfo        func(model string) *modelcatalog.ModelInfo
	RequestMode      func() string
}
```

Use callbacks when values can refresh during the session.

- [ ] **Step 6: Wire the runtime chat setup to provide those callbacks**

In `internal/runtime/chat.go`, pass existing data sources into `ChatLiveConfig` rather than duplicating fetch logic in `ChatModel`.

- [ ] **Step 7: Run the provider-summary tests and make them green**

Run: `go test ./internal/tui/... -run 'TestChatStatusSummary'`
Expected: PASS

- [ ] **Step 8: Commit the status-data plumbing**

```bash
git add internal/tui/chatstats.go internal/tui/chatstats_test.go internal/runtime/chat.go internal/tui/chatmodel.go internal/tui/chatshared.go
git commit -m "feat: add provider-aware chat status helpers"
```

---

### Task 3: Replace the Single-Line Header With the Approved Two-Line Status Surface

**Files:**
- Modify: `/Users/cass/git/forge/internal/tui/chatmodel.go`
- Modify: `/Users/cass/git/forge/internal/tui/chatmsg.go`
- Test: `/Users/cass/git/forge/internal/tui/chatmodel_test.go`

- [ ] **Step 1: Write failing header view tests**

```go
func TestChatModelViewShowsActiveModelAndThemeInHeader(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "copilot/gpt-5", WorkDir: "/tmp"})
	m.width, m.height = 120, 30
	m.themeID = "light"
	got := m.View()
	if !strings.Contains(got, "copilot/gpt-5") || !strings.Contains(got, "light") {
		t.Fatalf("view = %s", got)
	}
}

func TestChatModelViewShowsSecondStatusLine(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "copilot/gpt-5", WorkDir: "/tmp"})
	m.width, m.height = 120, 30
	m.statsUsage = llm.Usage{InputTokens: 100, OutputTokens: 20}
	got := m.View()
	if !strings.Contains(got, "latest") {
		t.Fatalf("view = %s", got)
	}
}
```

- [ ] **Step 2: Run the header tests to verify they fail**

Run: `go test ./internal/tui/... -run 'TestChatModelViewShowsActiveModelAndThemeInHeader|TestChatModelViewShowsSecondStatusLine'`
Expected: FAIL because the header is still one line and theme naming does not render

- [ ] **Step 3: Refactor header rendering into dedicated helpers**

In `chatmodel.go`, replace the direct single-line string assembly with:

- `renderHeader(theme chatTheme, data chatStatusData) string`
- line 1 from `buildStatusLine1`
- line 2 from `buildStatusLine2`

Use theme colors instead of hardcoded header colors.

- [ ] **Step 4: Keep the header compact and width-safe**

Clamp and truncate long values deliberately:

- workdir should use a shortened path helper where needed
- provider summary should be concise
- do not let line 2 wrap unpredictably

- [ ] **Step 5: Add a provider-conditioned view test**

```go
func TestChatModelViewShowsCopilotSummaryOnlyForCopilotModel(t *testing.T) {
	// Copilot model should show copilot detail
	// Anthropic model should not
}
```

- [ ] **Step 6: Run the header/view test subset and make it green**

Run: `go test ./internal/tui/... -run 'TestChatModelViewShowsActiveModelAndThemeInHeader|TestChatModelViewShowsSecondStatusLine|TestChatModelViewShowsCopilotSummaryOnlyForCopilotModel'`
Expected: PASS

- [ ] **Step 7: Commit the header/status surface**

```bash
git add internal/tui/chatmodel.go internal/tui/chatmodel_test.go internal/tui/chatmsg.go
git commit -m "feat: add provider-aware two-line chat header"
```

---

### Task 4: Expand `/stats` Into a Comprehensive Diagnostics Overlay

**Files:**
- Modify: `/Users/cass/git/forge/internal/tui/chatmodel.go`
- Modify: `/Users/cass/git/forge/internal/tui/chatstats.go`
- Modify: `/Users/cass/git/forge/internal/tui/chatstats_test.go`
- Modify: `/Users/cass/git/forge/internal/tui/chatmodel_test.go`
- Test: `/Users/cass/git/forge/internal/tui/chatstats_test.go`
- Test: `/Users/cass/git/forge/internal/tui/chatmodel_test.go`

- [ ] **Step 1: Write failing `/stats` section tests**

```go
func TestRenderStatsSectionsIncludesTurnSessionProviderModel(t *testing.T) {
	lines := renderStatsSections(chatStatusData{
		Model:        "copilot/gpt-5",
		LastUsage:    llm.Usage{InputTokens: 120, OutputTokens: 30},
		SessionUsage: llm.Usage{InputTokens: 200, OutputTokens: 40},
		RequestMode:  "responses",
	})
	joined := strings.Join(lines, "\n")
	for _, section := range []string{"Turn", "Session", "Provider", "Model"} {
		if !strings.Contains(joined, section) {
			t.Fatalf("missing section %q in %s", section, joined)
		}
	}
}
```

- [ ] **Step 2: Run the new `/stats` tests to verify they fail**

Run: `go test ./internal/tui/... -run 'TestRenderStatsSections|TestChatModelSlashStats'`
Expected: FAIL because the overlay is still token-only

- [ ] **Step 3: Implement section assembly helpers in `chatstats.go`**

Add small helpers:

- `renderTurnSection(data chatStatusData) []string`
- `renderSessionSection(data chatStatusData) []string`
- `renderProviderSection(data chatStatusData) []string`
- `renderModelSection(data chatStatusData) []string`
- `renderDiagnosticsSection(data chatStatusData) []string`

- [ ] **Step 4: Update the overlay renderer to use section output**

Replace the current fixed six-line implementation with a sectioned layout that:

- expands box height based on content up to terminal limits
- preserves the existing close semantics
- keeps content readable in both dark and light themes

- [ ] **Step 5: Add provider-specific overlay coverage**

Add at least:

- Copilot live quota section rendering
- Codex/OpenAI usage section rendering
- unavailable-data fallback rendering
- request-mode rendering
- model metadata rendering

- [ ] **Step 6: Run the `/stats` test subset and make it green**

Run: `go test ./internal/tui/... -run 'TestRenderStatsSections|TestChatModelSlashStats|TestChatModelStats'`
Expected: PASS

- [ ] **Step 7: Commit the `/stats` expansion**

```bash
git add internal/tui/chatmodel.go internal/tui/chatstats.go internal/tui/chatstats_test.go internal/tui/chatmodel_test.go
git commit -m "feat: expand chat stats overlay with provider diagnostics"
```

---

### Task 5: Polish, Persistence, and Full Regression Coverage

**Files:**
- Modify: `/Users/cass/git/forge/internal/tui/chatmodel.go`
- Modify: `/Users/cass/git/forge/internal/tui/chatshared.go`
- Modify: `/Users/cass/git/forge/internal/tui/chattheme_test.go`
- Modify: `/Users/cass/git/forge/internal/tui/chatstats_test.go`
- Modify: `/Users/cass/git/forge/internal/tui/chatmodel_test.go`

- [ ] **Step 1: Decide and implement what chat state persists**

Persist only useful user-facing state:

- active chat theme
- session totals already captured

Do not persist transient provider snapshots that should refresh live.

- [ ] **Step 2: Write failing persistence tests**

```go
func TestChatSessionSnapshotPersistsThemeID(t *testing.T) {
	// save session with non-default theme
	// restore and assert themeID round-trips
}
```

- [ ] **Step 3: Implement minimal snapshot updates**

Update `chatSessionSnapshot`, `snapshot()`, and `applySnapshot()` only for approved persisted fields.

- [ ] **Step 4: Run the targeted persistence tests**

Run: `go test ./internal/tui/... -run 'TestChatSessionSnapshotPersistsThemeID'`
Expected: PASS

- [ ] **Step 5: Run the full TUI suite**

Run: `go test ./internal/tui/...`
Expected: PASS

- [ ] **Step 6: Run the full repo suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 7: Commit the polish pass**

```bash
git add internal/tui/chatmodel.go internal/tui/chatshared.go internal/tui/chattheme_test.go internal/tui/chatstats_test.go internal/tui/chatmodel_test.go
git commit -m "test: cover chat status, stats, and theme regressions"
```

---

## Manual Verification Checklist

- [ ] Start chat with a Copilot model and confirm line 2 shows Copilot quota/allowance summary.
- [ ] Start chat with a ChatGPT/OpenAI/Codex path model and confirm line 2 switches to that provider’s usage summary.
- [ ] Start chat with another provider and confirm no irrelevant subscription text appears.
- [ ] Use `/theme` to cycle themes and confirm dark, low, light, and mid-contrast themes all render legibly.
- [ ] Use `/theme light` and confirm overlays, panes, and message chrome all follow the theme.
- [ ] Open `/stats` and confirm `Turn`, `Session`, `Provider`, `Model`, and `Diagnostics` sections appear.
- [ ] Verify `/stats` gracefully shows unavailable data for providers that do not expose a field.
- [ ] Save and restore a session and confirm the selected theme is restored if persistence was implemented.

---

## Final Verification

- [ ] **Step 1: Run formatting and tests**

Run: `go test ./internal/tui/... && go test ./...`
Expected: PASS

- [ ] **Step 2: Inspect git diff**

Run: `git diff --stat HEAD~5..HEAD`
Expected: only planned chat/theme/status/stats files changed

- [ ] **Step 3: Final commit if needed**

```bash
git add -A
git commit -m "feat: complete chat status, stats, and theme improvements"
```
