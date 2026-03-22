# Fix Session Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After a session completes, let the user describe what went wrong and re-run forge with the previous output files pre-seeded as context and fresh model selection.

**Architecture:** A new standalone Bubble Tea program (`RunPostSession`) replaces the current print-and-exit after `RunLive`. The Done screen gains an `f` key that transitions to a new `FixModel` screen (issue text box + writer/auditor selectors defaulting to the previous session's models). On submit, `main.go` copies the previous `code/` directory into the new session's writer, then loops back into `startSession` + `RunLive`.

**Tech Stack:** Go 1.22+, Bubble Tea (charmbracelet/bubbletea), tcell, internal `output` and `tui` packages.

---

## Chunk 1: Foundation — keys, messages, SeedFrom

### Task 1: Add `KeyFix` constant and `FixRequested` message

**Files:**
- Modify: `internal/tui/keys.go`
- Modify: `internal/tui/done.go`
- Modify: `internal/tui/done_test.go`

- [ ] **Step 1: Add `KeyFix` to keys.go**

  In `internal/tui/keys.go`, add after `KeyNewSession`:

  ```go
  KeyFix = "f"
  ```

- [ ] **Step 2: Add `FixRequested` type and handle `f` in DoneModel**

  In `internal/tui/done.go`, add the message type before `NewSessionRequested`:

  ```go
  // FixRequested is emitted when the user presses f on the done screen.
  type FixRequested struct{}
  ```

  In `DoneModel.Update`, inside the switch, add a case before the default return:

  ```go
  case KeyFix:
      if !m.Aborted {
          return m, func() tea.Msg { return FixRequested{} }
      }
  ```

  In `DoneModel.View`, conditionally add the `f  fix` hint only when not aborted. Replace the existing hint line:

  ```go
  // old:
  sb.WriteString("\n" + styleDim.Render("o  open in Finder   n  new session   q  quit") + "\n")
  // new (place this inside the else branch that already exists, after "Session complete"):
  if m.Aborted {
      sb.WriteString("\n" + styleDim.Render("o  open in Finder   n  new session   q  quit") + "\n")
  } else {
      sb.WriteString("\n" + styleDim.Render("o  open in Finder   n  new session   f  fix   q  quit") + "\n")
  }
  ```

- [ ] **Step 3: Write failing test**

  In `internal/tui/done_test.go`, add:

  ```go
  func TestDoneFixKey(t *testing.T) {
      m := tui.NewDoneModel("/tmp/output", false, "")
      _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
      if cmd == nil {
          t.Fatal("expected a command from pressing f")
      }
      msg := cmd()
      if _, ok := msg.(tui.FixRequested); !ok {
          t.Errorf("expected FixRequested, got %T", msg)
      }
  }

  func TestDoneFixKeyIgnoredWhenAborted(t *testing.T) {
      m := tui.NewDoneModel("/tmp/output", true, "some error")
      _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
      if cmd != nil {
          t.Error("expected no command when session is aborted, but got one")
      }
  }
  ```

- [ ] **Step 4: Run tests to verify they pass**

  ```
  cd ~/git/forge && go test ./internal/tui/... -run TestDoneFix -v
  ```
  Expected: PASS for both TestDoneFixKey and TestDoneFixKeyIgnoredWhenAborted

- [ ] **Step 5: Commit**

  ```bash
  cd ~/git/forge
  git add internal/tui/keys.go internal/tui/done.go internal/tui/done_test.go
  git commit -m "feat(tui): add f key on done screen to emit FixRequested"
  ```

---

### Task 2: Add `output.Writer.SeedFrom`

**Files:**
- Modify: `internal/output/writer.go`
- Modify: `internal/output/writer_test.go`

- [ ] **Step 1: Write failing test**

  In `internal/output/writer_test.go`, add:

  ```go
  func TestSeedFrom(t *testing.T) {
      base := t.TempDir()

      // Create source writer with some files
      src, _ := output.NewWriter(base, time.Now())
      src.WriteCode(output.CodeBlock{Filename: "main.go", Content: "package main\n"})
      src.WriteCode(output.CodeBlock{Filename: "sub/util.go", Content: "package sub\n"})

      // Create destination writer and seed it
      dst, _ := output.NewWriter(base, time.Now().Add(time.Second))
      if err := dst.SeedFrom(filepath.Join(src.Dir(), "code")); err != nil {
          t.Fatalf("SeedFrom: %v", err)
      }

      // Both files should appear in dst's code dir
      for _, name := range []string{"main.go", "sub/util.go"} {
          data, err := os.ReadFile(filepath.Join(dst.Dir(), "code", name))
          if err != nil {
              t.Fatalf("expected seeded file %s: %v", name, err)
          }
          if len(data) == 0 {
              t.Errorf("seeded file %s is empty", name)
          }
      }
  }

  func TestSeedFromMissingDir(t *testing.T) {
      base := t.TempDir()
      dst, _ := output.NewWriter(base, time.Now())
      // Non-existent source dir should return nil (nothing to seed).
      if err := dst.SeedFrom("/no/such/path/code"); err != nil {
          t.Errorf("SeedFrom non-existent dir should be a no-op, got: %v", err)
      }
  }
  ```

- [ ] **Step 2: Run to verify failure**

  ```
  cd ~/git/forge && go test ./internal/output/... -run TestSeedFrom -v
  ```
  Expected: FAIL — `SeedFrom` undefined

- [ ] **Step 3: Implement `SeedFrom` in writer.go**

  Add after `InlineCodeFiles` in `internal/output/writer.go`:

  ```go
  // SeedFrom copies all files from srcCodeDir into this writer's code/ subdirectory.
  // If srcCodeDir does not exist the call is a no-op.
  func (w *Writer) SeedFrom(srcCodeDir string) error {
      if _, err := os.Stat(srcCodeDir); os.IsNotExist(err) {
          return nil
      }
      return filepath.WalkDir(srcCodeDir, func(path string, d os.DirEntry, err error) error {
          if err != nil || d.IsDir() {
              return err
          }
          rel, _ := filepath.Rel(srcCodeDir, path)
          dst := filepath.Join(w.dir, "code", rel)
          if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
              return err
          }
          data, err := os.ReadFile(path)
          if err != nil {
              return err
          }
          return os.WriteFile(dst, data, 0o644)
      })
  }
  ```

- [ ] **Step 4: Run tests to verify they pass**

  ```
  cd ~/git/forge && go test ./internal/output/... -run TestSeedFrom -v
  ```
  Expected: PASS

- [ ] **Step 5: Run full output package tests**

  ```
  cd ~/git/forge && go test ./internal/output/... -v
  ```
  Expected: all PASS

- [ ] **Step 6: Commit**

  ```bash
  cd ~/git/forge
  git add internal/output/writer.go internal/output/writer_test.go
  git commit -m "feat(output): add Writer.SeedFrom to copy prior code/ into new session"
  ```

---

## Chunk 2: FixModel screen

### Task 3: Create `FixModel` in `internal/tui/fix.go`

**Files:**
- Create: `internal/tui/fix.go`
- Create: `internal/tui/fix_test.go`

- [ ] **Step 1: Write failing tests**

  Create `internal/tui/fix_test.go`:

  ```go
  package tui_test

  import (
      "strings"
      "testing"

      "forge/internal/tui"
      tea "github.com/charmbracelet/bubbletea"
  )

  func TestFixModelEnterEmitsFixStarted(t *testing.T) {
      m := tui.NewFixModel("/tmp/out", tui.SessionStarted{
          WriterModel:  "claude-sonnet-4-6",
          AuditorModel: "gpt-4o",
      }, []string{"claude-sonnet-4-6", "gpt-4o"}, []string{"claude-sonnet-4-6", "gpt-4o"})

      // Type an issue
      for _, ch := range "login breaks" {
          m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
          m = m2.(tui.FixModel)
      }

      // Press enter
      m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
      _ = m2
      if cmd == nil {
          t.Fatal("expected command on enter")
      }
      msg := cmd()
      fs, ok := msg.(tui.FixStarted)
      if !ok {
          t.Fatalf("expected FixStarted, got %T", msg)
      }
      if fs.Issue != "login breaks" {
          t.Errorf("issue = %q, want %q", fs.Issue, "login breaks")
      }
      if fs.WriterModel != "claude-sonnet-4-6" {
          t.Errorf("writer = %q, want claude-sonnet-4-6", fs.WriterModel)
      }
  }

  func TestFixModelEnterNoopWhenEmpty(t *testing.T) {
      m := tui.NewFixModel("/tmp/out", tui.SessionStarted{
          WriterModel:  "claude-sonnet-4-6",
          AuditorModel: "gpt-4o",
      }, []string{"claude-sonnet-4-6"}, []string{"gpt-4o"})

      _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
      if cmd != nil {
          t.Error("expected no command when issue is empty")
      }
  }

  func TestFixModelTabSwitchesFocus(t *testing.T) {
      m := tui.NewFixModel("/tmp/out", tui.SessionStarted{
          WriterModel:  "claude-sonnet-4-6",
          AuditorModel: "gpt-4o",
      }, []string{"claude-sonnet-4-6"}, []string{"gpt-4o"})
      if m.ModelFocus != 0 {
          t.Fatal("expected initial focus on writer")
      }
      m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
      fm := m2.(tui.FixModel)
      if fm.ModelFocus != 1 {
          t.Errorf("expected focus to shift to auditor after tab")
      }
  }

  func TestFixModelViewContainsOutputDir(t *testing.T) {
      m := tui.NewFixModel("/my/output/dir", tui.SessionStarted{
          WriterModel:  "claude-sonnet-4-6",
          AuditorModel: "gpt-4o",
      }, []string{"claude-sonnet-4-6"}, []string{"gpt-4o"})
      view := m.View()
      if !strings.Contains(view, "/my/output/dir") {
          t.Errorf("view should show output dir, got:\n%s", view)
      }
  }
  ```

- [ ] **Step 2: Run to verify failure**

  ```
  cd ~/git/forge && go test ./internal/tui/... -run TestFixModel -v
  ```
  Expected: FAIL — compile error, `FixModel` and `FixStarted` not defined

- [ ] **Step 3: Implement `fix.go`**

  Create `internal/tui/fix.go`:

  ```go
  package tui

  import (
      "strings"

      tea "github.com/charmbracelet/bubbletea"
  )

  // FixStarted is emitted when the user submits the fix description.
  type FixStarted struct {
      Issue        string
      WriterModel  string
      AuditorModel string
  }

  // FixModel is the Bubble Tea model for the fix-session screen.
  type FixModel struct {
      SourceOutputDir string
      Issue           string
      WriterModels    []string
      AuditorModels   []string
      WriterIdx       int
      AuditorIdx      int
      ModelFocus      int // 0 = writer, 1 = auditor
      Width           int
  }

  func NewFixModel(sourceOutputDir string, last SessionStarted, writerModels, auditorModels []string) FixModel {
      m := FixModel{
          SourceOutputDir: sourceOutputDir,
          WriterModels:    writerModels,
          AuditorModels:   auditorModels,
      }
      if idx := indexOf(writerModels, last.WriterModel); idx >= 0 {
          m.WriterIdx = idx
      }
      if idx := indexOf(auditorModels, last.AuditorModel); idx >= 0 {
          m.AuditorIdx = idx
      }
      return m
  }

  func (m FixModel) Init() tea.Cmd { return nil }

  func (m FixModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
      if sz, ok := msg.(tea.WindowSizeMsg); ok {
          m.Width = sz.Width
          return m, nil
      }
      key, ok := msg.(tea.KeyMsg)
      if !ok {
          return m, nil
      }
      switch key.String() {
      case "ctrl+c":
          return m, tea.Quit
      case "tab":
          m.ModelFocus = 1 - m.ModelFocus
      case "left":
          if m.ModelFocus == 0 && len(m.WriterModels) > 0 {
              m.WriterIdx = (m.WriterIdx - 1 + len(m.WriterModels)) % len(m.WriterModels)
          } else if m.ModelFocus == 1 && len(m.AuditorModels) > 0 {
              m.AuditorIdx = (m.AuditorIdx - 1 + len(m.AuditorModels)) % len(m.AuditorModels)
          }
      case "right":
          if m.ModelFocus == 0 && len(m.WriterModels) > 0 {
              m.WriterIdx = (m.WriterIdx + 1) % len(m.WriterModels)
          } else if m.ModelFocus == 1 && len(m.AuditorModels) > 0 {
              m.AuditorIdx = (m.AuditorIdx + 1) % len(m.AuditorModels)
          }
      case "enter":
          if strings.TrimSpace(m.Issue) != "" {
              issue := m.Issue
              writer := m.writerModel()
              auditor := m.auditorModel()
              return m, func() tea.Msg {
                  return FixStarted{Issue: issue, WriterModel: writer, AuditorModel: auditor}
              }
          }
      case "backspace":
          if len(m.Issue) > 0 {
              m.Issue = m.Issue[:len(m.Issue)-1]
          }
      default:
          if len(key.String()) == 1 {
              m.Issue += key.String()
          }
      }
      return m, nil
  }

  func (m FixModel) writerModel() string {
      if len(m.WriterModels) == 0 {
          return ""
      }
      return m.WriterModels[m.WriterIdx]
  }

  func (m FixModel) auditorModel() string {
      if len(m.AuditorModels) == 0 {
          return ""
      }
      return m.AuditorModels[m.AuditorIdx]
  }

  func (m FixModel) View() string {
      var sb strings.Builder
      sb.WriteString(styleBold.Render("forge") + "  " + styleDim.Render("fix session") + "\n\n")

      // Show the output dir being fixed
      sb.WriteString(styleDim.Render("Source  ") + styleMid.Render(m.SourceOutputDir) + "\n\n")

      // Model rows
      for i, label := range []string{"Writer  ", "Auditor "} {
          var modelName string
          if i == 0 {
              modelName = m.writerModel()
          } else {
              modelName = m.auditorModel()
          }
          if m.ModelFocus == i {
              sb.WriteString(styleGreen.Render("▶") + " " + styleDim.Render(label) + " " + styleBright.Render(modelName))
              sb.WriteString(styleDim.Render("  ← tab · ← → cycle"))
          } else {
              sb.WriteString("  " + styleDim.Render(label) + " " + styleMid.Render(modelName))
          }
          sb.WriteString("\n")
      }

      // Issue box
      boxWidth := m.Width - 4
      if boxWidth < 40 {
          boxWidth = 40
      }
      if boxWidth > 100 {
          boxWidth = 100
      }
      innerWidth := boxWidth
      dashes := strings.Repeat("─", boxWidth+2)

      sb.WriteString("\n")
      sb.WriteString(styleDimGreen.Render("what went wrong?") + "\n")
      sb.WriteString(styleDim.Render("┌"+dashes+"┐") + "\n")

      remaining := append([]rune(m.Issue), '_')
      for len(remaining) > 0 {
          var chunk []rune
          if len(remaining) >= innerWidth {
              chunk = remaining[:innerWidth]
              remaining = remaining[innerWidth:]
          } else {
              chunk = remaining
              remaining = nil
          }
          pad := strings.Repeat(" ", innerWidth-len(chunk))
          sb.WriteString(styleDim.Render("│") + " " + styleBright.Render(string(chunk)) + pad + " " + styleDim.Render("│") + "\n")
      }
      sb.WriteString(styleDim.Render("└"+dashes+"┘") + "\n")

      if strings.TrimSpace(m.Issue) != "" {
          sb.WriteString(styleDimGreen.Render("↵") + styleDim.Render(" Fix  ·  ") + styleDim.Render("^C Quit") + "\n")
      } else {
          sb.WriteString(styleDim.Render("describe the issue...") + "\n")
      }
      return sb.String()
  }
  ```

- [ ] **Step 4: Run tests**

  ```
  cd ~/git/forge && go test ./internal/tui/... -run TestFixModel -v
  ```
  Expected: all PASS

- [ ] **Step 5: Commit**

  ```bash
  cd ~/git/forge
  git add internal/tui/fix.go internal/tui/fix_test.go
  git commit -m "feat(tui): add FixModel screen for fix-session prompt entry"
  ```

---

## Chunk 3: PostRunApp and RunPostSession

### Task 4: Create `PostRunApp` and `RunPostSession` in `internal/tui/postrun.go`

**Files:**
- Create: `internal/tui/postrun.go`
- Create: `internal/tui/postrun_test.go`

- [ ] **Step 1: Write failing tests**

  Create `internal/tui/postrun_test.go`:

  ```go
  package tui_test

  import (
      "testing"

      "forge/internal/tui"
      tea "github.com/charmbracelet/bubbletea"
  )

  func makePostRunApp(aborted bool) tui.PostRunApp {
      return tui.NewPostRunApp(
          "/tmp/out",
          aborted,
          "",
          tui.SessionStarted{WriterModel: "claude-sonnet-4-6", AuditorModel: "gpt-4o", Rounds: 3},
          []string{"claude-sonnet-4-6", "gpt-4o"},
          []string{"claude-sonnet-4-6", "gpt-4o"},
      )
  }

  func TestPostRunAppInitialScreenIsDone(t *testing.T) {
      app := makePostRunApp(false)
      if app.Screen != tui.PostRunScreenDone {
          t.Errorf("expected PostRunScreenDone, got %v", app.Screen)
      }
  }

  func TestPostRunAppFixTransition(t *testing.T) {
      app := makePostRunApp(false)
      app2, _ := app.Update(tui.FixRequested{})
      pa := app2.(tui.PostRunApp)
      if pa.Screen != tui.PostRunScreenFix {
          t.Errorf("expected PostRunScreenFix after FixRequested, got %v", pa.Screen)
      }
  }

  func TestPostRunAppFixStartedQuitsWithResult(t *testing.T) {
      app := makePostRunApp(false)
      // transition to fix screen
      app2, _ := app.Update(tui.FixRequested{})
      app = app2.(tui.PostRunApp)

      // submit fix — capture the returned model so result is populated
      app3, cmd := app.Update(tui.FixStarted{
          Issue:        "broken auth",
          WriterModel:  "claude-sonnet-4-6",
          AuditorModel: "gpt-4o",
      })
      app = app3.(tui.PostRunApp)
      if cmd == nil {
          t.Fatal("expected quit command")
      }
      _ = cmd() // tea.QuitMsg
      if app.Result().Fix != true {
          t.Error("expected Fix=true in result")
      }
      if app.Result().Issue != "broken auth" {
          t.Errorf("issue = %q", app.Result().Issue)
      }
  }
  ```

- [ ] **Step 2: Run to verify failure**

  ```
  cd ~/git/forge && go test ./internal/tui/... -run TestPostRunApp -v
  ```
  Expected: FAIL — compile error

- [ ] **Step 3: Implement `postrun.go`**

  Create `internal/tui/postrun.go`:

  ```go
  package tui

  import (
      tea "github.com/charmbracelet/bubbletea"
  )

  type PostRunScreen int

  const (
      PostRunScreenDone PostRunScreen = iota
      PostRunScreenFix
  )

  // PostRunResult is returned by RunPostSession to tell main.go what to do next.
  type PostRunResult struct {
      Fix          bool
      Issue        string
      WriterModel  string
      AuditorModel string
  }

  // PostRunApp is the Bubble Tea root model for the post-session UI.
  type PostRunApp struct {
      Screen PostRunScreen
      done   DoneModel
      fix    FixModel
      result PostRunResult
      width  int
      height int
  }

  func NewPostRunApp(
      outputDir string,
      aborted bool,
      reason string,
      lastStart SessionStarted,
      writerModels, auditorModels []string,
  ) PostRunApp {
      return PostRunApp{
          Screen: PostRunScreenDone,
          done:   NewDoneModel(outputDir, aborted, reason),
          fix:    NewFixModel(outputDir, lastStart, writerModels, auditorModels),
      }
  }

  func (a PostRunApp) Result() PostRunResult { return a.result }

  func (a PostRunApp) Init() tea.Cmd { return nil }

  func (a PostRunApp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
      if sz, ok := msg.(tea.WindowSizeMsg); ok {
          a.width = sz.Width
          a.height = sz.Height
          a.fix.Width = sz.Width
          return a, nil
      }

      switch msg := msg.(type) {
      case FixRequested:
          a.Screen = PostRunScreenFix
          return a, nil
      case FixStarted:
          a.result = PostRunResult{
              Fix:          true,
              Issue:        msg.Issue,
              WriterModel:  msg.WriterModel,
              AuditorModel: msg.AuditorModel,
          }
          return a, tea.Quit
      }

      switch a.Screen {
      case PostRunScreenDone:
          updated, cmd := a.done.Update(msg)
          a.done = updated.(DoneModel)
          return a, cmd
      case PostRunScreenFix:
          updated, cmd := a.fix.Update(msg)
          a.fix = updated.(FixModel)
          return a, cmd
      }
      return a, nil
  }

  func (a PostRunApp) View() string {
      switch a.Screen {
      case PostRunScreenDone:
          return a.done.View()
      case PostRunScreenFix:
          return a.fix.View()
      }
      return ""
  }

  // RunPostSession runs the post-session UI (done + optional fix screen).
  // It blocks until the user quits or submits a fix.
  func RunPostSession(
      outputDir string,
      aborted bool,
      reason string,
      lastStart SessionStarted,
      writerModels, auditorModels []string,
  ) PostRunResult {
      app := NewPostRunApp(outputDir, aborted, reason, lastStart, writerModels, auditorModels)
      p := tea.NewProgram(app, tea.WithAltScreen())
      retModel, err := p.Run()
      if err != nil {
          return PostRunResult{}
      }
      final, _ := retModel.(PostRunApp)
      return final.Result()
  }
  ```

- [ ] **Step 4: Run tests**

  ```
  cd ~/git/forge && go test ./internal/tui/... -run TestPostRunApp -v
  ```
  Expected: all PASS

- [ ] **Step 5: Run full tui tests**

  ```
  cd ~/git/forge && go test ./internal/tui/... -v
  ```
  Expected: all PASS

- [ ] **Step 6: Commit**

  ```bash
  cd ~/git/forge
  git add internal/tui/postrun.go internal/tui/postrun_test.go
  git commit -m "feat(tui): add PostRunApp and RunPostSession for done+fix screens"
  ```

---

## Chunk 4: Wire it all together in main.go

### Task 5: Update `main.go` to loop with fix support

**Files:**
- Modify: `cmd/forge/main.go`

- [ ] **Step 1: Add `seedFrom` parameter to `startSession`**

  Change the `startSession` signature to accept an optional seed directory. In `cmd/forge/main.go`:

  ```go
  // old signature:
  func startSession(ctx context.Context, cfg *config.Config, tokens *auth.Tokens, reg *llm.Registry, started tui.SessionStarted, gate *session.TurnGate) (<-chan llm.Event, string) {

  // new signature:
  func startSession(ctx context.Context, cfg *config.Config, tokens *auth.Tokens, reg *llm.Registry, started tui.SessionStarted, gate *session.TurnGate, seedFrom string) (<-chan llm.Event, string) {
  ```

  Inside `startSession`, after `w` is created and before the goroutine is started, add:

  ```go
  if seedFrom != "" {
      if err := w.SeedFrom(seedFrom); err != nil {
          go func() {
              events <- llm.Event{Kind: llm.EventAbort, Err: fmt.Errorf("seed from prior output: %w", err)}
              close(events)
          }()
          return events, w.Dir()
      }
  }
  ```

- [ ] **Step 2: Update the first `startSession` call**

  In `main()`, update the initial call to pass an empty seed:

  ```go
  // old:
  events, outDir := startSession(ctx, cfg, tokens, reg, finalApp.LastStart(), gate)
  // new:
  events, outDir := startSession(ctx, cfg, tokens, reg, finalApp.LastStart(), gate, "")
  ```

- [ ] **Step 3: Replace the post-RunLive print with a RunPostSession loop**

  Replace the code after `RunLive` (the error handling + print):

  ```go
  // OLD (remove this block):
  result := tui.RunLive(events, 4, finalApp.LastStart().Rounds, tui.LiveConfig{...}, outDir)
  if result.Err != nil {
      fmt.Fprintf(os.Stderr, "session error (...): %v\n", result.Err)
  }
  if result.Aborted {
      fmt.Fprintf(os.Stderr, "session aborted\noutput: %s\n", result.OutputDir)
      return
  }
  fmt.Printf("session complete\noutput: %s\n", result.OutputDir)
  ```

  Replace with this loop:

  ```go
  lastStart := finalApp.LastStart()
  for {
      result := tui.RunLive(events, 4, lastStart.Rounds, tui.LiveConfig{
          WriterModel:  lastStart.WriterModel,
          AuditorModel: lastStart.AuditorModel,
          Gate:         gate,
      }, outDir)

      aborted := result.Aborted
      reason := ""
      if result.Err != nil {
          reason = result.Err.Error()
      }

      post := tui.RunPostSession(outDir, aborted, reason, lastStart, available, available)
      if !post.Fix {
          break
      }

      lastStart = tui.SessionStarted{
          Prompt:       post.Issue,
          WriterModel:  post.WriterModel,
          AuditorModel: post.AuditorModel,
          Rounds:       lastStart.Rounds,
          LangHint:     lastStart.LangHint,
      }
      gate = session.NewTurnGate() // fresh gate — don't carry manual-mode state across sessions
      events, outDir = startSession(ctx, cfg, tokens, reg, lastStart, gate, filepath.Join(outDir, "code"))
  }
  ```

  Note: `available` is already computed earlier in `main()`. Move its declaration above the `app :=` line if needed so it's in scope here.

- [ ] **Step 4: Build to verify no compile errors**

  ```
  cd ~/git/forge && go build ./...
  ```
  Expected: no errors

- [ ] **Step 5: Run all tests**

  ```
  cd ~/git/forge && go test ./...
  ```
  Expected: all PASS

- [ ] **Step 6: Commit**

  ```bash
  cd ~/git/forge
  git add cmd/forge/main.go
  git commit -m "feat: wire fix-session loop — done screen f-key seeds and reruns session"
  ```

---

## Chunk 5: Smoke test and lint

### Task 6: Lint and verify build

**Files:** none (verification only)

- [ ] **Step 1: Run the linter**

  ```
  cd ~/git/forge && go vet ./...
  ```
  Expected: no output (no issues)

- [ ] **Step 2: Run the full test suite one final time**

  ```
  cd ~/git/forge && go test ./... -count=1
  ```
  Expected: all PASS, no skips

- [ ] **Step 3: Sanity-check the binary starts**

  ```
  cd ~/git/forge && go build -o /tmp/forge-test ./cmd/forge && echo "build ok"
  ```
  Expected: `build ok`

- [ ] **Step 4: Clean up temp binary**

  ```
  rm /tmp/forge-test
  ```
