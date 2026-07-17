package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"forge/internal/agent"
	"forge/internal/agent/tools"
	"forge/internal/auth"
	"forge/internal/chatstate"
	"forge/internal/config"
	"forge/internal/hooks"
	"forge/internal/llm"
	"forge/internal/mcp"
	"forge/internal/protocol"
	reactruntime "forge/internal/react"
	"forge/internal/sessionstore"
	"forge/internal/skills"
	"forge/internal/tui"
)

type discardRuntimeRenderTarget struct{}

func (discardRuntimeRenderTarget) AgentToken(string)                       {}
func (discardRuntimeRenderTarget) AgentText(string)                        {}
func (discardRuntimeRenderTarget) ToolCall(string, string)                 {}
func (discardRuntimeRenderTarget) ToolResult(string, string, string, bool) {}
func (discardRuntimeRenderTarget) Stats(time.Duration, llm.Usage)          {}
func (discardRuntimeRenderTarget) Error(string)                            {}
func (discardRuntimeRenderTarget) Info(string)                             {}

type contextRuntimeRenderTarget struct {
	discardRuntimeRenderTarget
	contextUsed int
	usage       llm.Usage
}

func (r *contextRuntimeRenderTarget) StatsWithContext(_ time.Duration, usage llm.Usage, contextUsed int) {
	r.usage = usage
	r.contextUsed = contextUsed
}

func TestToolContractsDoNotUseJSONInStringParameters(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
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

func TestStructuredToolsExposeNativeSchema(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
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

func TestModelVisibleToolContractsDoNotContainKnownBadPlaceholders(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, session, approve, nil, nil)

	bad := []string{"TODO", "steps_json", "options_json", "<tool_call>"}
	for _, tool := range reg.All() {
		contract := struct {
			Name        string
			Description string
			Parameters  []tools.ParameterDef
			Schema      any
		}{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
			Schema:      tool.Schema,
		}
		blob, err := json.Marshal(contract)
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

func TestRegisteredGitCommitUsesSideEffectIntentAllowlist(t *testing.T) {
	workDir := t.TempDir()
	runRuntimeGit(t, workDir, "init")
	runRuntimeGit(t, workDir, "config", "user.email", "test@example.com")
	runRuntimeGit(t, workDir, "config", "user.name", "Test User")
	writeRuntimeTestFile(t, filepath.Join(workDir, "main.go"), "package main\n")
	runRuntimeGit(t, workDir, "add", "main.go")
	runRuntimeGit(t, workDir, "commit", "-m", "initial")
	runRuntimeGit(t, workDir, "branch", "-M", "main")
	writeRuntimeTestFile(t, filepath.Join(workDir, "FORGE_VS_CODEX.md"), "doc\n")
	writeRuntimeTestFile(t, filepath.Join(workDir, "unrelated.go"), "package main\n")

	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, workDir, cfg, session, approve, nil, nil)
	session.SetSideEffectIntent(reactruntime.SideEffectIntent{AllowedPaths: []string{"FORGE_VS_CODEX.md"}, TargetBranch: "main", Remote: "origin"})

	tool, ok := reg.Get("git_commit")
	if !ok {
		t.Fatal("git_commit not registered")
	}
	result, err := tool.Execute(context.Background(), map[string]any{"message": "add comparison"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "commit") {
		t.Fatalf("result = %q", result)
	}
	files := runtimeGitOut(t, workDir, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, "FORGE_VS_CODEX.md") || strings.Contains(files, "unrelated.go") {
		t.Fatalf("commit files = %q", files)
	}
}

func TestRegisteredGitPushUsesSideEffectIntentRemoteAndTarget(t *testing.T) {
	workDir := t.TempDir()
	runRuntimeGit(t, workDir, "init")
	runRuntimeGit(t, workDir, "config", "user.email", "test@example.com")
	runRuntimeGit(t, workDir, "config", "user.name", "Test User")
	writeRuntimeTestFile(t, filepath.Join(workDir, "main.go"), "package main\n")
	runRuntimeGit(t, workDir, "add", "main.go")
	runRuntimeGit(t, workDir, "commit", "-m", "initial")
	runRuntimeGit(t, workDir, "branch", "-M", "main")
	remote := filepath.Join(t.TempDir(), "remote.git")
	runRuntimeGit(t, t.TempDir(), "init", "--bare", remote)
	runRuntimeGit(t, workDir, "remote", "add", "origin", remote)

	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, workDir, cfg, session, approve, nil, nil)
	session.SetSideEffectIntent(reactruntime.SideEffectIntent{AllowedPaths: []string{"main.go"}, TargetBranch: "main", Remote: "origin"})

	tool, ok := reg.Get("git_push")
	if !ok {
		t.Fatal("git_push not registered")
	}
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "remote contains") {
		t.Fatalf("result = %q", result)
	}
}

func TestGitScopeProviderRequiresBranchWhenTargetBranchPresent(t *testing.T) {
	session := reactruntime.NewSession()
	session.SetSideEffectIntent(reactruntime.SideEffectIntent{
		AllowedPaths: []string{"FORGE_VS_CODEX.md"},
		TargetBranch: "main",
		Remote:       "origin",
		RequiredActions: []reactruntime.SideEffectAction{
			reactruntime.SideEffectActionCommit,
			reactruntime.SideEffectActionPush,
		},
	})

	scope := gitScopeProviderForSession(session)()
	if !scope.RequireBranch {
		t.Fatalf("RequireBranch = false, want true when target branch is present")
	}
}

func writeRuntimeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runtimeGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %s\n%s", args, err, out)
	}
	return string(out)
}

func runRuntimeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = runtimeGitOut(t, dir, args...)
}

func TestToolSchemaFixtureMatchesGenerated(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, session, approve, nil, nil)

	generated := map[string]any{}
	for _, tool := range reg.All() {
		if tool.Schema == nil {
			continue
		}
		generated[tool.Name] = protocol.ToolSchemaToJSONSchema(tool.Schema)
	}
	encoded, err := json.MarshalIndent(generated, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(filepath.Join("..", "protocol", "schemas", "forge_tools.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(expected) != string(encoded)+"\n" {
		t.Fatalf("tool schema fixture differs; regenerate internal/protocol/schemas/forge_tools.schema.json\n%s", encoded)
	}
}

func TestChatRuntimeCreatesDurableThreadStoreWhenOutputDirConfigured(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Session.OutputDir = t.TempDir()
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, session, approve, nil, nil)
	if session.DurableSink() == nil {
		t.Fatal("expected durable sink to be configured")
	}
}

func TestRegisterToolsWriteFileUsesActiveWorkspaceRoot(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "arkanoid")
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	session.SetActiveWorkspaceRoot(workspace)
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, base, cfg, session, approve, nil, nil)

	tool, ok := reg.Get("write_file")
	if !ok {
		t.Fatal("missing write_file tool")
	}
	_, err = tool.Execute(context.Background(), map[string]any{
		"path": filepath.Join(workspace, "index.html"), "content": "game",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "game" {
		t.Fatalf("file content = %q, want game", string(data))
	}
}

func TestRegisterToolsEditFileUsesActiveWorkspaceRoot(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "arkanoid")
	path := filepath.Join(workspace, "index.html")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old game"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	session.SetActiveWorkspaceRoot(workspace)
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, base, cfg, session, approve, nil, nil)

	tool, ok := reg.Get("edit_file")
	if !ok {
		t.Fatal("missing edit_file tool")
	}
	_, err = tool.Execute(context.Background(), map[string]any{
		"path": path, "old_text": "old game", "new_text": "new game",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new game" {
		t.Fatalf("file content = %q, want new game", string(data))
	}
}

func TestRegisterToolsApplyPatchUsesActiveWorkspaceRoot(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "arkanoid")
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	session.SetActiveWorkspaceRoot(workspace)
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, base, cfg, session, approve, nil, nil)

	tool, ok := reg.Get("apply_patch")
	if !ok {
		t.Fatal("missing apply_patch tool")
	}
	_, err = tool.Execute(context.Background(), map[string]any{"patch": `diff --git a/index.html b/index.html
new file mode 100644
--- /dev/null
+++ b/index.html
@@ -0,0 +1 @@
+game`})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "game\n" {
		t.Fatalf("file content = %q", string(data))
	}
}

func TestRegisterToolsRunCommandUsesActiveWorkspaceRoot(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "arkanoid")
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	session.SetActiveWorkspaceRoot(workspace)
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, base, cfg, session, approve, nil, nil)

	tool, ok := reg.Get("run_command")
	if !ok {
		t.Fatal("missing run_command tool")
	}
	result, err := tool.Execute(context.Background(), map[string]any{"command": "pwd"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, workspace) || !strings.Contains(result, "exit 0") {
		t.Fatalf("run_command result = %q, want active workspace pwd", result)
	}
}

func TestRegisterToolsGitStatusUsesActiveWorkspaceRoot(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "arkanoid")
	mustRunChatGit(t, base, "init")
	if err := os.WriteFile(filepath.Join(base, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRunChatGit(t, workspace, "init")
	if err := os.WriteFile(filepath.Join(workspace, "active.txt"), []byte("active\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	session.SetActiveWorkspaceRoot(workspace)
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, base, cfg, session, approve, nil, nil)

	tool, ok := reg.Get("git_status")
	if !ok {
		t.Fatal("missing git_status tool")
	}
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "active.txt") || strings.Contains(result, "base.txt") {
		t.Fatalf("git_status result = %q, want active workspace only", result)
	}
}

func mustRunChatGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestRegisterToolsExecSessionStartUsesActiveWorkspaceRoot(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "arkanoid")
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	session.SetActiveWorkspaceRoot(workspace)
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, base, cfg, session, approve, nil, nil)

	startTool, ok := reg.Get("exec_session_start")
	if !ok {
		t.Fatal("missing exec_session_start tool")
	}
	statusTool, ok := reg.Get("exec_session_status")
	if !ok {
		t.Fatal("missing exec_session_status tool")
	}
	startResult, err := startTool.Execute(context.Background(), map[string]any{"command": "pwd", "cols": 80, "rows": 24})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		SessionID int `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(startResult), &started); err != nil {
		t.Fatalf("start payload = %q: %v", startResult, err)
	}
	if started.SessionID == 0 {
		t.Fatalf("start payload = %q, missing session id", startResult)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		statusResult, err := statusTool.Execute(context.Background(), map[string]any{"session_id": started.SessionID})
		if err != nil {
			t.Fatal(err)
		}
		var status struct {
			Output string `json:"output"`
		}
		if err := json.Unmarshal([]byte(statusResult), &status); err != nil {
			t.Fatalf("status payload = %q: %v", statusResult, err)
		}
		if strings.Contains(status.Output, workspace) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for exec session pwd in workspace, last status: %s", statusResult)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestChatRuntimeCreatesDurableThreadFiles(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	outputDir := t.TempDir()
	cfg.Session.OutputDir = outputDir
	cfg.Chat.Model = "test/model"
	workDir := t.TempDir()
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, workDir, cfg, session, approve, nil, nil)
	session.RecordInput("hello durable runtime")

	threadsDir := filepath.Join(outputDir, "threads")
	entries, err := os.ReadDir(threadsDir)
	if err != nil {
		t.Fatal(err)
	}
	var threadID string
	var hasMeta bool
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".jsonl") {
			threadID = strings.TrimSuffix(name, ".jsonl")
		}
		if strings.HasSuffix(name, ".meta.json") {
			hasMeta = true
		}
	}
	if threadID == "" || !hasMeta {
		t.Fatalf("threadID=%q hasMeta=%v entries=%#v", threadID, hasMeta, entries)
	}
	store := sessionstore.NewJSONLThreadStore(threadsDir)
	items, err := store.ReadItems(context.Background(), threadID)
	if err != nil {
		t.Fatal(err)
	}
	var foundMeta, foundInput bool
	for _, item := range items {
		if item.Kind == protocol.ItemSessionMeta && item.SessionMeta != nil && item.SessionMeta.CWD == workDir && item.SessionMeta.Model == "test/model" {
			foundMeta = true
		}
		if item.Kind == protocol.ItemUserMessage && item.Message != nil && item.Message.Text == "hello durable runtime" {
			foundInput = true
		}
	}
	if !foundMeta || !foundInput {
		t.Fatalf("foundMeta=%v foundInput=%v items=%#v", foundMeta, foundInput, items)
	}
}

func TestResolveModelExactAndIndexed(t *testing.T) {
	models := []string{"alpha/model-a", "beta/model-b", "gamma/model-c"}
	if got := ResolveModel(models, "2"); got != "beta/model-b" {
		t.Fatalf("expected indexed model, got %q", got)
	}
	if got := ResolveModel(models, "gamma/model-c"); got != "gamma/model-c" {
		t.Fatalf("expected exact model, got %q", got)
	}
}

func TestAgentProgressRendererRecordsToolCalls(t *testing.T) {
	var gotName, gotSummary string
	renderer := newAgentProgressRenderTarget(discardRuntimeRenderTarget{}, func(name, summary string) {
		gotName = name
		gotSummary = summary
	})

	renderer.ToolCall("read_file", "README.md")

	if gotName != "read_file" || gotSummary != "README.md" {
		t.Fatalf("recorded progress = %q %q", gotName, gotSummary)
	}
}

func TestAgentProgressRendererForwardsContextStats(t *testing.T) {
	target := &contextRuntimeRenderTarget{}
	renderer := newAgentProgressRenderTarget(target, func(name, summary string) {})
	contextRenderer, ok := renderer.(agent.ContextStatsTarget)
	if !ok {
		t.Fatalf("wrapped renderer should preserve ContextStatsTarget support")
	}

	contextRenderer.StatsWithContext(time.Second, llm.Usage{InputTokens: 12, OutputTokens: 3}, 456)

	if target.contextUsed != 456 {
		t.Fatalf("contextUsed = %d, want 456", target.contextUsed)
	}
	if target.usage.InputTokens != 12 || target.usage.OutputTokens != 3 {
		t.Fatalf("usage = %#v", target.usage)
	}
}

func TestResolveModelAmbiguousSubstringReturnsEmpty(t *testing.T) {
	models := []string{"openai/gpt-4o", "openai/gpt-4o-mini"}
	if got := ResolveModel(models, "gpt-4o"); got != "openai/gpt-4o" {
		t.Fatalf("expected exact match to win, got %q", got)
	}
	if got := ResolveModel(models, "openai"); got != "" {
		t.Fatalf("expected ambiguous substring to return empty, got %q", got)
	}
}

func TestRunOutcomeReturnsFailedSignalAndEmitsError(t *testing.T) {
	events := make(chan llm.Event, 1)
	renderer := agent.NewEventRenderer(events)

	outcome := func(err error) string {
		if err != nil {
			renderer.Error(err.Error())
			return "__turn_failed__"
		}
		return "__turn_done__"
	}

	if got := outcome(assertErr("quota")); got != "__turn_failed__" {
		t.Fatalf("got %q", got)
	}
	ev := <-events
	if ev.Kind != llm.EventError || ev.Text != "quota" {
		t.Fatalf("unexpected event: %#v", ev)
	}
}

func TestRunOutcomeReturnsDoneSignalWithoutError(t *testing.T) {
	events := make(chan llm.Event, 1)
	renderer := agent.NewEventRenderer(events)

	outcome := func(err error) string {
		if err != nil {
			renderer.Error(err.Error())
			return "__turn_failed__"
		}
		return "__turn_done__"
	}

	if got := outcome(nil); got != "__turn_done__" {
		t.Fatalf("got %q", got)
	}
	select {
	case ev := <-events:
		t.Fatalf("unexpected event: %#v", ev)
	default:
	}
}

func TestRefreshChatSetupStateReloadsConfigAndTokens(t *testing.T) {
	oldLoadConfig := loadChatConfig
	oldLoadTokens := loadChatTokens
	defer func() {
		loadChatConfig = oldLoadConfig
		loadChatTokens = oldLoadTokens
	}()

	loadChatConfig = func() (*config.Config, error) {
		cfg := &config.Config{}
		cfg.Keys.OpenAI = "reloaded"
		return cfg, nil
	}
	loadChatTokens = func() (*auth.Tokens, error) {
		return &auth.Tokens{CopilotToken: "copilot-token"}, nil
	}

	setup := &ChatSetup{Config: &config.Config{}}
	cfg, tokens := refreshChatSetupState(setup)

	if cfg.Keys.OpenAI != "reloaded" {
		t.Fatalf("cfg.Keys.OpenAI = %q, want reloaded", cfg.Keys.OpenAI)
	}
	if setup.Config.Keys.OpenAI != "reloaded" {
		t.Fatalf("setup.Config.Keys.OpenAI = %q, want reloaded", setup.Config.Keys.OpenAI)
	}
	if tokens.CopilotToken != "copilot-token" {
		t.Fatalf("tokens.CopilotToken = %q, want copilot-token", tokens.CopilotToken)
	}
}

func TestPersistChatLastModelUpdatesConfigAndWritesState(t *testing.T) {
	cfg := &config.Config{}
	var savedPath string
	var savedModel string

	oldSave := saveLastChatModel
	oldPath := defaultConfigPath
	defer func() {
		saveLastChatModel = oldSave
		defaultConfigPath = oldPath
	}()

	saveLastChatModel = func(path, model string) error {
		savedPath = path
		savedModel = model
		return nil
	}
	defaultConfigPath = func() string {
		return filepath.Join(t.TempDir(), "config.toml")
	}

	persistChatLastModel(cfg, "claude/claude-sonnet-4-6")

	if cfg.Chat.LastModel != "claude/claude-sonnet-4-6" {
		t.Fatalf("cfg.Chat.LastModel = %q", cfg.Chat.LastModel)
	}
	if savedModel != "claude/claude-sonnet-4-6" {
		t.Fatalf("saved model = %q", savedModel)
	}
	if savedPath == "" {
		t.Fatal("expected config path to be used")
	}
}

func TestBuildChatSetupAllowsNoConfiguredModels(t *testing.T) {
	oldLoadTokens := loadChatTokens
	defer func() {
		loadChatTokens = oldLoadTokens
	}()

	loadChatTokens = func() (*auth.Tokens, error) {
		return &auth.Tokens{}, nil
	}

	cfg, err := config.Load("/nonexistent/path.toml")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	setup, err := BuildChatSetup(cfg, nil, "", t.TempDir(), false)
	if err != nil {
		t.Fatalf("BuildChatSetup: %v", err)
	}
	if setup == nil {
		t.Fatal("expected setup")
	}
	if setup.ChatModel != "" {
		t.Fatalf("ChatModel = %q, want empty", setup.ChatModel)
	}
	if setup.Driver != nil {
		t.Fatal("expected nil driver when no provider is configured")
	}
	if len(setup.Providers) == 0 {
		t.Fatal("expected provider options for in-app configuration")
	}
}

func TestPrintChatHelpOmitsExpandCommand(t *testing.T) {
	output := captureRuntimeStdout(t, PrintChatHelp)
	if strings.Contains(output, "/expand") {
		t.Fatalf("expected /expand to be removed from chat help, got:\n%s", output)
	}
}

func TestPrintChatHelpMentionsDebugViewAndTraceCommand(t *testing.T) {
	output := captureRuntimeStdout(t, PrintChatHelp)
	if !strings.Contains(output, "forge -d") {
		t.Fatalf("expected chat help to mention forge -d, got:\n%s", output)
	}
	if !strings.Contains(output, "/trace") {
		t.Fatalf("expected chat help to mention /trace, got:\n%s", output)
	}
}

func TestPrintChatHelpMentionsNewSessionCommand(t *testing.T) {
	output := captureRuntimeStdout(t, PrintChatHelp)
	if !strings.Contains(output, "/new") {
		t.Fatalf("expected chat help to mention /new, got:\n%s", output)
	}
}

func TestHandleChatSlashCommandExpandIsUnknown(t *testing.T) {
	var buf bytes.Buffer
	renderer := agent.NewRenderer(&buf, 80, false)
	setup := &ChatSetup{}

	if handled := handleChatSlashCommand("/expand", renderer, nil, nil, nil, setup); !handled {
		t.Fatal("expected slash command to be handled")
	}
	if !strings.Contains(buf.String(), "unknown command: /expand") {
		t.Fatalf("expected unknown command output, got %q", buf.String())
	}
}

func TestHandleChatSlashCommandSkillArgumentsSubstitution(t *testing.T) {
	var buf bytes.Buffer
	renderer := agent.NewRenderer(&buf, 80, false)
	session := &stubChatSessionControl{}
	state := chatstate.New()
	loaded := []skills.Skill{{Name: "pr", Body: "Open a PR titled: $ARGUMENTS"}}

	if handled := handleChatSlashCommand("/pr fix the login bug", renderer, loaded, state, session, &ChatSetup{}); !handled {
		t.Fatal("expected slash command to be handled")
	}
	if session.lastSkillBody != "Open a PR titled: fix the login bug" {
		t.Fatalf("skill body = %q", session.lastSkillBody)
	}
}

func TestHandleChatSlashCommandSkillArgumentsAppendedWhenNoPlaceholder(t *testing.T) {
	var buf bytes.Buffer
	renderer := agent.NewRenderer(&buf, 80, false)
	session := &stubChatSessionControl{}
	state := chatstate.New()
	loaded := []skills.Skill{{Name: "review", Body: "Review the diff."}}

	if handled := handleChatSlashCommand("/review focus on error handling", renderer, loaded, state, session, &ChatSetup{}); !handled {
		t.Fatal("expected slash command to be handled")
	}
	if session.lastSkillBody != "Review the diff.\n\nfocus on error handling" {
		t.Fatalf("skill body = %q", session.lastSkillBody)
	}
}

func TestHandleChatSlashCommandClearAlsoClearsReactSession(t *testing.T) {
	var buf bytes.Buffer
	renderer := agent.NewRenderer(&buf, 80, false)
	session := &stubChatSessionControl{}
	state := chatstate.New()
	state.ActivateSkill("brainstorming")

	if handled := handleChatSlashCommand("/clear", renderer, nil, state, session, &ChatSetup{}); !handled {
		t.Fatal("expected slash command to be handled")
	}
	if !session.cleared {
		t.Fatal("expected react session clear to be invoked")
	}
	if got := state.ActiveSkills(); len(got) != 0 {
		t.Fatalf("active skills after clear = %#v", got)
	}
}

func TestHandleChatSlashCommandNewClearsReactSession(t *testing.T) {
	var buf bytes.Buffer
	renderer := agent.NewRenderer(&buf, 80, false)
	session := &stubChatSessionControl{}
	state := chatstate.New()
	state.ActivateSkill("brainstorming")

	if handled := handleChatSlashCommand("/new", renderer, nil, state, session, &ChatSetup{}); !handled {
		t.Fatal("expected slash command to be handled")
	}
	if !session.cleared {
		t.Fatal("expected react session clear to be invoked")
	}
	if got := state.ActiveSkills(); len(got) != 0 {
		t.Fatalf("active skills after new session = %#v", got)
	}
	if !strings.Contains(buf.String(), "new session started") {
		t.Fatalf("expected new-session output, got %q", buf.String())
	}
}

func TestHandleChatSlashCommandModelAlsoUpdatesReactSessionDriver(t *testing.T) {
	var buf bytes.Buffer
	renderer := agent.NewRenderer(&buf, 80, false)
	session := &stubChatSessionControl{}
	setup := &ChatSetup{
		Available: []string{"openai/gpt-5.4"},
		MakeDriver: func(name string) llm.Driver {
			return &kernelMockDriver{response: name}
		},
	}

	if handled := handleChatSlashCommand("/model openai/gpt-5.4", renderer, nil, nil, session, setup); !handled {
		t.Fatal("expected slash command to be handled")
	}
	if session.driver == nil {
		t.Fatal("expected react session driver to be updated")
	}
	if setup.ChatModel != "openai/gpt-5.4" {
		t.Fatalf("chat model = %q", setup.ChatModel)
	}
}

func TestLoadChatApprovalConfigWiresAutoClassifier(t *testing.T) {
	cfg := &config.Config{}
	cfg.Permissions.Auto.Enabled = true
	cfg.Permissions.Auto.Model = "classifier-model"
	cfg.Permissions.Auto.MaxConsecutiveDenials = 2
	cfg.Permissions.Auto.MaxTotalDenials = 10
	cfg.Permissions.Auto.FailureBehavior = "deny"
	requested := ""
	setup := &ChatSetup{
		Config: cfg,
		MakeDriver: func(name string) llm.Driver {
			requested = name
			return &kernelMockDriver{response: `{"decision":"allow","reason":"safe"}`}
		},
	}

	approvalCfg := loadChatApprovalConfig(setup)
	if requested != "classifier-model" {
		t.Fatalf("requested model = %q", requested)
	}
	if approvalCfg.Classifier == nil {
		t.Fatal("expected classifier")
	}
	if approvalCfg.Denials == nil {
		t.Fatal("expected denial tracker")
	}
	if approvalCfg.ClassifierFailureBehavior != reactruntime.ClassifierFailureDeny {
		t.Fatalf("failure behavior = %q", approvalCfg.ClassifierFailureBehavior)
	}
}

func TestLoadChatApprovalConfigUsesClassifierTimeoutMS(t *testing.T) {
	cfg := &config.Config{}
	cfg.Permissions.Auto.Enabled = true
	cfg.Permissions.Auto.Model = "classifier-model"
	cfg.Permissions.Auto.TimeoutMS = 250
	setup := &ChatSetup{
		Config: cfg,
		MakeDriver: func(name string) llm.Driver {
			return &kernelMockDriver{response: `{"decision":"allow","reason":"safe"}`}
		},
	}

	approvalCfg := loadChatApprovalConfig(setup)
	classifier, ok := approvalCfg.Classifier.(*llmPermissionClassifier)
	if !ok {
		t.Fatalf("classifier = %T, want *llmPermissionClassifier", approvalCfg.Classifier)
	}
	if classifier.timeout != 250*time.Millisecond {
		t.Fatalf("classifier timeout = %s, want 250ms", classifier.timeout)
	}
}

func TestHandleChatSlashCommandCompact(t *testing.T) {
	var buf bytes.Buffer
	renderer := agent.NewRenderer(&buf, 80, false)
	session := &stubChatSessionControl{compactChanged: true}

	if handled := handleChatSlashCommand("/compact", renderer, nil, nil, session, &ChatSetup{}); !handled {
		t.Fatal("expected slash command to be handled")
	}
	if session.compactKeep != 1 {
		t.Fatalf("compact keep = %d, want 1", session.compactKeep)
	}
	if !strings.Contains(buf.String(), "compacted conversation history") {
		t.Fatalf("expected compact message, got %q", buf.String())
	}
}

func TestHandleChatSlashCommandCompactRecent(t *testing.T) {
	var buf bytes.Buffer
	renderer := agent.NewRenderer(&buf, 80, false)
	session := &stubChatSessionControl{compactChanged: true}

	if handled := handleChatSlashCommand("/compact recent 20", renderer, nil, nil, session, &ChatSetup{}); !handled {
		t.Fatal("expected slash command to be handled")
	}
	if session.compactKeep != 20 {
		t.Fatalf("compact keep = %d, want 20", session.compactKeep)
	}
	if !strings.Contains(buf.String(), "preserved recent 20 turns") {
		t.Fatalf("expected compact recent message, got %q", buf.String())
	}
}

func TestHandleChatSlashCommandCompactStatus(t *testing.T) {
	var buf bytes.Buffer
	renderer := agent.NewRenderer(&buf, 80, false)
	session := &stubChatSessionControl{compactStatus: "3 compacted turns; summary length 12"}

	if handled := handleChatSlashCommand("/compact status", renderer, nil, nil, session, &ChatSetup{}); !handled {
		t.Fatal("expected slash command to be handled")
	}
	if !strings.Contains(buf.String(), "3 compacted turns") {
		t.Fatalf("expected compact status, got %q", buf.String())
	}
}

func TestHandleChatSlashCommandActivatesSkillInReactSession(t *testing.T) {
	var buf bytes.Buffer
	renderer := agent.NewRenderer(&buf, 80, false)
	session := &stubChatSessionControl{}
	state := chatstate.New()
	loadedSkills := []skills.Skill{{
		Name:        "brainstorming",
		Description: "plan before implementation",
		Body:        "Use brainstorming.",
	}}

	if handled := handleChatSlashCommand("/brainstorming", renderer, loadedSkills, state, session, &ChatSetup{}); !handled {
		t.Fatal("expected slash command to be handled")
	}
	if !state.SkillActivated("brainstorming") {
		t.Fatal("expected skill to be activated")
	}
	if session.lastUserMessage != "" {
		t.Fatalf("skill activation appended user message %q, want typed skill context only", session.lastUserMessage)
	}
	if session.lastSkillName != "brainstorming" || session.lastSkillBody != "Use brainstorming." {
		t.Fatalf("skill context = %q/%q", session.lastSkillName, session.lastSkillBody)
	}
}

func TestRunChatTurnRecordsSkillContextWithoutRunningEmptySkillText(t *testing.T) {
	runner := &stubChatTurnRunner{}
	err := runChatTurn(context.Background(), runner, chatstate.ChatUserInput{IsInput: true, SkillName: "brainstorming", SkillBody: "Write docs/plans/design.md"})
	if err != nil {
		t.Fatal(err)
	}
	if runner.calls != 0 {
		t.Fatalf("Run calls = %d, want 0 for skill activation only", runner.calls)
	}
	if runner.skillName != "brainstorming" || !strings.Contains(runner.skillBody, "docs/plans/design.md") {
		t.Fatalf("skill context = %q/%q", runner.skillName, runner.skillBody)
	}
}

func TestRunChatTurnKeepsOriginalTextSeparateFromSkillContext(t *testing.T) {
	runner := &stubChatTurnRunner{}
	err := runChatTurn(context.Background(), runner, chatstate.ChatUserInput{IsInput: true, Text: "design /buddy", SkillName: "brainstorming", SkillBody: "Write docs/plans/design.md"})
	if err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 || runner.input != "design /buddy" {
		t.Fatalf("Run calls/input = %d/%q", runner.calls, runner.input)
	}
	if runner.skillName != "brainstorming" || !strings.Contains(runner.skillBody, "docs/plans/design.md") {
		t.Fatalf("skill context = %q/%q", runner.skillName, runner.skillBody)
	}
}

func TestAutoSkillChatInputKeepsSkillContextSeparate(t *testing.T) {
	state := chatstate.New()
	loadedSkills := []skills.Skill{{Name: "brainstorming", Description: "Plan first", Body: "Write docs/plans/design.md"}}

	ui, ok := autoSkillChatInput(loadedSkills, state, "design /buddy")

	if !ok {
		t.Fatal("expected auto skill input")
	}
	if ui.Text != "design /buddy" || ui.SkillName != "brainstorming" || !strings.Contains(ui.SkillBody, "docs/plans/design.md") {
		t.Fatalf("auto skill input = %#v", ui)
	}
	if strings.Contains(ui.Text, "[Skill:") {
		t.Fatalf("skill body leaked into user text: %q", ui.Text)
	}
}

func TestResolveChatRuntimeModeReadsEnv(t *testing.T) {
	t.Setenv("FORGE_CHAT_RUNTIME", "")
	if got := resolveChatRuntimeMode(); got != chatRuntimeReact {
		t.Fatalf("mode = %q, want %q", got, chatRuntimeReact)
	}

	t.Setenv("FORGE_CHAT_RUNTIME", "legacy")
	if got := resolveChatRuntimeMode(); got != chatRuntimeReact {
		t.Fatalf("mode = %q, want %q", got, chatRuntimeReact)
	}

	t.Setenv("FORGE_CHAT_RUNTIME", "react")
	if got := resolveChatRuntimeMode(); got != chatRuntimeReact {
		t.Fatalf("mode = %q, want %q", got, chatRuntimeReact)
	}

	t.Setenv("FORGE_CHAT_RUNTIME", " ReAcT ")
	if got := resolveChatRuntimeMode(); got != chatRuntimeReact {
		t.Fatalf("mode = %q, want %q", got, chatRuntimeReact)
	}

	t.Setenv("FORGE_CHAT_RUNTIME", "unexpected")
	if got := resolveChatRuntimeMode(); got != chatRuntimeReact {
		t.Fatalf("mode = %q, want %q", got, chatRuntimeReact)
	}
}

func TestRunChatLiveUsesSurfaceMode(t *testing.T) {
	t.Setenv("FORGE_CHAT_RUNTIME", "react")

	oldRunChatLiveUI := runChatLiveUI
	defer func() {
		runChatLiveUI = oldRunChatLiveUI
	}()

	var got []tui.ChatLiveConfig
	runChatLiveUI = func(_ <-chan llm.Event, cfg tui.ChatLiveConfig, inputCh chan<- string, _ <-chan struct{}) tui.ChatLiveResult {
		got = append(got, cfg)
		close(inputCh)
		return tui.ChatLiveResult{}
	}

	setup := &ChatSetup{
		Config:    &config.Config{},
		ChatModel: "openai/gpt-5.4",
		WorkDir:   t.TempDir(),
		Driver:    &kernelMockDriver{response: "ok"},
	}

	RunChatLive(setup)
	setup.debugRec = &chatDebugRecorder{}
	RunChatLive(setup)

	if len(got) != 2 {
		t.Fatalf("surface modes captured = %d, want 2", len(got))
	}
	if got[0].SurfaceKind != tui.ChatSurfaceDefault {
		t.Fatalf("default surface kind = %q", got[0].SurfaceKind)
	}
	if got[1].SurfaceKind != tui.ChatSurfaceDebug {
		t.Fatalf("debug surface kind = %q", got[1].SurfaceKind)
	}
	if got[0].DebugEnabled {
		t.Fatalf("default debug enabled = %v", got[0].DebugEnabled)
	}
	if !got[1].DebugEnabled {
		t.Fatalf("debug debug enabled = %v", got[1].DebugEnabled)
	}

	defaultMode := got[0].SurfaceMode()
	debugMode := got[1].SurfaceMode()
	if defaultMode.UseAltScreen {
		t.Fatalf("default surface mode = %#v", defaultMode)
	}
	if !defaultMode.EnableMouseCapture {
		t.Fatalf("default surface mode should enable mouse capture = %#v", defaultMode)
	}
	if debugMode.UseAltScreen {
		t.Fatalf("debug surface mode = %#v", debugMode)
	}
	if !debugMode.EnableMouseCapture {
		t.Fatalf("debug surface mode should enable mouse capture = %#v", debugMode)
	}
	if !defaultMode.EnableBracketedPaste || !defaultMode.EnableLiveRegion {
		t.Fatalf("default surface missing required flags: %#v", defaultMode)
	}
	if !debugMode.EnableBracketedPaste || !debugMode.EnableLiveRegion {
		t.Fatalf("debug surface missing required flags: %#v", debugMode)
	}
}

func TestRunChatTurnUsesReactRunnerWhenProvided(t *testing.T) {
	reactRunner := &stubChatTurnRunner{}
	if err := runChatTurn(context.Background(), reactRunner, chatstate.ChatUserInput{IsInput: true, Text: "describe this directory"}); err != nil {
		t.Fatal(err)
	}
	if reactRunner.calls != 1 {
		t.Fatalf("react runner calls = %d, want 1", reactRunner.calls)
	}
	if reactRunner.input != "describe this directory" {
		t.Fatalf("react runner input = %q", reactRunner.input)
	}
}

func TestRunChatTurnReturnsErrorForTypedNilReactRunner(t *testing.T) {
	var typedNilRunner *reactruntime.Runner
	err := runChatTurn(context.Background(), typedNilRunner, chatstate.ChatUserInput{IsInput: true, Text: "describe this directory"})
	if err == nil {
		t.Fatal("expected error when react runner is nil")
	}
}

func TestSuggestedSkillNudgePrefersModeAwareSkill(t *testing.T) {
	loaded := []skills.Skill{
		{Name: "brainstorming"},
		{Name: "test-driven-development"},
	}
	state := chatstate.New()

	got := suggestedSkillNudge("please implement the runtime change", loaded, state)
	if !strings.Contains(got, "test-driven-development") {
		t.Fatalf("nudge = %q", got)
	}
}

func TestSuggestedSkillNudgeSkipsActiveSkill(t *testing.T) {
	loaded := []skills.Skill{
		{Name: "brainstorming"},
	}
	state := chatstate.New()
	state.ActivateSkill("brainstorming")

	if got := suggestedSkillNudge("plan this change", loaded, state); got != "" {
		t.Fatalf("nudge = %q, want empty", got)
	}
}

func TestSuggestedSkillNudgeSkipsCrossRepoComparisonDoc(t *testing.T) {
	loaded := []skills.Skill{
		{Name: "test-driven-development"},
	}
	state := chatstate.New()
	input := "take a look at this repo and compare it to the tier 1 operators like claude, codex, opencode, deepseek. The code is in ~/git, codex, cci, opencode, deepseek. look at forges features and write me a nice doc when your done"

	if got := suggestedSkillNudge(input, loaded, state); got != "" {
		t.Fatalf("nudge = %q, want empty", got)
	}
}

func TestChatSuggestedSkillHookProducesOverlay(t *testing.T) {
	results := suggestedSkillPromptHook(context.Background(), hooks.Event{
		Point: hooks.PointPromptContext,
		Transient: chatPromptHookPayload{
			SuggestedSkillNudge: "suggested skill: /test-driven-development (implementation request matched)",
		},
	})

	if len(results) != 1 {
		t.Fatalf("results = %#v", results)
	}
	overlay, ok := results[0].(hooks.OverlayResult)
	if !ok {
		t.Fatalf("result type = %T, want hooks.OverlayResult", results[0])
	}
	if overlay.Key != "suggested_skill" {
		t.Fatalf("overlay key = %q", overlay.Key)
	}
	if !strings.Contains(overlay.Content, "/test-driven-development") {
		t.Fatalf("overlay content = %q", overlay.Content)
	}
}

func TestChatGuardianWarningHookProducesOverlay(t *testing.T) {
	results := guardianWarningPromptHook(context.Background(), hooks.Event{
		Point: hooks.PointPromptContext,
		Transient: chatPromptHookPayload{
			GuardianEvent: &reactruntime.GuardianEvent{
				Decision: tools.GuardianWarn,
				Reason:   "high-impact command has no compact task context",
				Action: tools.Action{
					Tool:    "run_command",
					Summary: "git merge feature/runtime",
				},
			},
		},
	})

	if len(results) != 1 {
		t.Fatalf("results = %#v", results)
	}
	overlay, ok := results[0].(hooks.OverlayResult)
	if !ok {
		t.Fatalf("result type = %T, want hooks.OverlayResult", results[0])
	}
	if overlay.Key != "guardian_warning" {
		t.Fatalf("overlay key = %q", overlay.Key)
	}
	if !strings.Contains(overlay.Content, "high-impact command") {
		t.Fatalf("overlay content = %q", overlay.Content)
	}
}

func TestApplySuggestedSkillOverlayAddsHookOverlay(t *testing.T) {
	session := reactruntime.NewSession()
	loaded := []skills.Skill{
		{Name: "test-driven-development"},
	}
	state := chatstate.New()

	applySuggestedSkillOverlay(session, "please implement the runtime change", loaded, state)

	snap := session.Snapshot()
	if !snap.HookOutputSet {
		t.Fatal("expected normalized hook output to be set")
	}
	if len(snap.HookOutput.Overlays) != 1 {
		t.Fatalf("hook output overlays = %#v", snap.HookOutput.Overlays)
	}
	if !strings.Contains(snap.HookOutput.Overlays[0].Content, "/test-driven-development") {
		t.Fatalf("hook output overlay = %#v", snap.HookOutput.Overlays[0])
	}
}

func TestApplySuggestedSkillOverlayClearsWhenNoSuggestion(t *testing.T) {
	session := reactruntime.NewSession()
	session.SetHookOverlays([]reactruntime.HookOverlay{{
		Key:        "suggested_skill",
		Content:    "old",
		Priority:   reactruntime.HookPriorityNormal,
		Provenance: "runtime",
	}})
	state := chatstate.New()

	applySuggestedSkillOverlay(session, "describe this repo", nil, state)

	snap := session.Snapshot()
	if !snap.HookOutputSet {
		t.Fatal("expected normalized hook output to stay authoritative")
	}
	if got := snap.HookOutput.Overlays; len(got) != 0 {
		t.Fatalf("hook output overlays = %#v", got)
	}
}

func TestApplySuggestedSkillOverlayPreservesOtherPromptOverlays(t *testing.T) {
	session := reactruntime.NewSession()
	session.SetHookOutput(hooks.ExecutionOutput{
		Overlays: []hooks.OverlayResult{
			{
				Key:        "guardian_warning",
				Content:    "guardian warning",
				Priority:   hooks.PriorityHigh,
				Provenance: "runtime",
			},
			{
				Key:        "git_workflow",
				Content:    "loop-owned overlay",
				Priority:   hooks.PriorityHigh,
				Provenance: "runtime",
			},
		},
		Failures: []hooks.Failure{{Handler: "stale"}},
		Block:    &hooks.BlockResult{Message: "stale block"},
	})
	state := chatstate.New()

	applySuggestedSkillOverlay(session, "please implement the runtime change", []skills.Skill{{Name: "test-driven-development"}}, state)

	snap := session.Snapshot()
	if got := snap.HookOutput.Block; got != nil {
		t.Fatalf("expected prompt-only merge to clear stale block, got %#v", got)
	}
	if got := snap.HookOutput.Failures; len(got) != 0 {
		t.Fatalf("expected prompt-only merge to clear stale failures, got %#v", got)
	}
	if got := len(snap.HookOutput.Overlays); got != 3 {
		t.Fatalf("hook output overlays = %#v", snap.HookOutput.Overlays)
	}
	if !containsHookOverlay(snap.HookOutput.Overlays, "guardian_warning", "guardian warning") {
		t.Fatalf("hook output overlays = %#v", snap.HookOutput.Overlays)
	}
	if !containsHookOverlay(snap.HookOutput.Overlays, "git_workflow", "loop-owned overlay") {
		t.Fatalf("hook output overlays = %#v", snap.HookOutput.Overlays)
	}
	if !containsHookOverlay(snap.HookOutput.Overlays, "suggested_skill", "/test-driven-development") {
		t.Fatalf("hook output overlays = %#v", snap.HookOutput.Overlays)
	}
}

func TestApplyGuardianOverlayAddsWarningHook(t *testing.T) {
	session := reactruntime.NewSession()

	applyGuardianOverlay(session, reactruntime.GuardianEvent{
		Decision: tools.GuardianWarn,
		Reason:   "high-impact command has no compact task context",
		Action: tools.Action{
			Tool:    "run_command",
			Summary: "git merge feature/runtime",
		},
	})

	snap := session.Snapshot()
	if !snap.HookOutputSet {
		t.Fatal("expected normalized hook output to be set")
	}
	got := snap.HookOutput.Overlays
	if len(got) != 1 {
		t.Fatalf("hook output overlays = %#v", got)
	}
	if got[0].Key != "guardian_warning" || !strings.Contains(got[0].Content, "high-impact command") {
		t.Fatalf("hook output overlays = %#v", got)
	}
}

func TestApplyGuardianOverlayClearsOnAllow(t *testing.T) {
	session := reactruntime.NewSession()
	session.SetHookOverlay(reactruntime.HookOverlay{
		Key:        "guardian_warning",
		Content:    "old warning",
		Priority:   reactruntime.HookPriorityHigh,
		Provenance: "runtime",
	})

	applyGuardianOverlay(session, reactruntime.GuardianEvent{Decision: tools.GuardianAllow})

	snap := session.Snapshot()
	if !snap.HookOutputSet {
		t.Fatal("expected normalized hook output to stay authoritative")
	}
	if got := snap.HookOutput.Overlays; len(got) != 0 {
		t.Fatalf("hook output overlays = %#v", got)
	}
}

func TestApplyGuardianOverlayClearsOwnedKeyButPreservesOthers(t *testing.T) {
	session := reactruntime.NewSession()
	session.SetHookOutput(hooks.ExecutionOutput{
		Overlays: []hooks.OverlayResult{
			{
				Key:        "suggested_skill",
				Content:    "suggested skill: /brainstorming (planning work benefits from explicit design before implementation)",
				Priority:   hooks.PriorityNormal,
				Provenance: "runtime",
			},
			{
				Key:        "guardian_warning",
				Content:    "old guardian warning",
				Priority:   hooks.PriorityHigh,
				Provenance: "runtime",
			},
			{
				Key:        "git_workflow",
				Content:    "loop-owned overlay",
				Priority:   hooks.PriorityHigh,
				Provenance: "runtime",
			},
		},
	})

	applyGuardianOverlay(session, reactruntime.GuardianEvent{Decision: tools.GuardianAllow})

	snap := session.Snapshot()
	if got := len(snap.HookOutput.Overlays); got != 2 {
		t.Fatalf("hook output overlays = %#v", snap.HookOutput.Overlays)
	}
	if !containsHookOverlay(snap.HookOutput.Overlays, "suggested_skill", "/brainstorming") {
		t.Fatalf("hook output overlays = %#v", snap.HookOutput.Overlays)
	}
	if !containsHookOverlay(snap.HookOutput.Overlays, "git_workflow", "loop-owned overlay") {
		t.Fatalf("hook output overlays = %#v", snap.HookOutput.Overlays)
	}
	if containsHookOverlay(snap.HookOutput.Overlays, "guardian_warning", "old guardian warning") {
		t.Fatalf("guardian warning should have been cleared: %#v", snap.HookOutput.Overlays)
	}
}

func containsHookOverlay(overlays []hooks.OverlayResult, key, content string) bool {
	for _, overlay := range overlays {
		if overlay.Key == key && strings.Contains(overlay.Content, content) {
			return true
		}
	}
	return false
}

func TestRunChatTurnCompletesComplexVisiblePreviewTurn(t *testing.T) {
	workDir := writeTranscriptFixtureRepo(t)
	cfg := &config.Config{}

	approve := agent.YoloApproval()

	reg := tools.NewRegistry()
	previewRuntime, _ := registerTools(reg, workDir, cfg, reactruntime.NewSession(), approve, nil, nil)
	if previewRuntime != nil {
		defer previewRuntime.Close()
	}
	baseReg := reg.Filter(nil)

	driver := &scriptedTranscriptDriver{}
	renderer := agent.NewRenderer(io.Discard, 80, false)
	reactRunner := reactruntime.NewRunner(reactruntime.Config{
		Driver:       driver,
		Tools:        reg,
		Renderer:     renderer,
		SystemPrompt: func() string { return agent.BuildNativeSystemPrompt(workDir) },
		Session:      reactruntime.NewSession(),
	})
	registerReactDelegationTools(reg, &ChatSetup{Config: cfg, WorkDir: workDir, Driver: driver}, baseReg, approve, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := "i dont like the current theme, i need you to mock up 3 new ones, dark in nature, really modern and cool looking, create a web server and show me them on the screen"
	if err := runChatTurn(ctx, reactRunner, chatstate.ChatUserInput{IsInput: true, Text: input}); err != nil {
		t.Fatalf("runChatTurn failed after %d driver calls with unexpected=%#v: %v", driver.calls, driver.unexpected, err)
	}
	if got := reactRunner.LastResponse(); !strings.Contains(got, "http://127.0.0.1:") || !strings.Contains(got, "themes_preview.html") {
		t.Fatalf("response = %q", got)
	}
	if len(driver.unexpected) > 0 {
		t.Fatalf("unexpected driver paths: %#v", driver.unexpected)
	}
}

func TestRegisterToolsIncludesPreviewLifecycleTools(t *testing.T) {
	reg := tools.NewRegistry()
	cfg := &config.Config{}
	_, _ = registerTools(reg, t.TempDir(), cfg, reactruntime.NewSession(), agent.YoloApproval(), nil, nil)

	for _, name := range []string{"artifact_write", "artifact_read", "preview_server_ensure", "preview_server_status"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("missing %s in tool registry", name)
		}
	}
}

func TestRegisterToolsIncludesHiddenReadOutputWhenOutputStoreConfigured(t *testing.T) {
	reg := tools.NewRegistry()
	cfg := &config.Config{}
	cfg.Session.OutputDir = t.TempDir()
	_, _ = registerTools(reg, t.TempDir(), cfg, reactruntime.NewSession(), agent.YoloApproval(), nil, nil)

	tool, ok := reg.Get("read_output")
	if !ok {
		t.Fatal("read_output tool not registered")
	}
	if tool.PromptVisibility != tools.PromptHidden {
		t.Fatalf("PromptVisibility = %v, want PromptHidden", tool.PromptVisibility)
	}
}

func TestRegisterToolsIncludesExecSessionLifecycleTools(t *testing.T) {
	reg := tools.NewRegistry()
	cfg := &config.Config{}
	_, _ = registerTools(reg, t.TempDir(), cfg, reactruntime.NewSession(), agent.YoloApproval(), nil, nil)

	for _, name := range []string{"exec_session_start", "exec_session_status", "exec_session_write", "exec_session_resize", "exec_session_stop"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("%s tool not registered", name)
		}
	}
}

func TestRegisterReactDelegationToolsAddsSpawnWaitAndOutputAlias(t *testing.T) {
	reg := tools.NewRegistry()
	cfg := &config.Config{}
	workDir := t.TempDir()
	approve := agent.YoloApproval()
	_, _ = registerTools(reg, workDir, cfg, reactruntime.NewSession(), approve, nil, nil)
	baseReg := reg.Filter(nil)

	setup := &ChatSetup{
		Config:     cfg,
		WorkDir:    workDir,
		MakeDriver: func(name string) llm.Driver { return &kernelMockDriver{response: "ok"} },
	}

	registerReactDelegationTools(reg, setup, baseReg, approve, nil, nil)
	if _, ok := reg.Get("spawn_agent"); !ok {
		t.Fatal("spawn_agent tool not registered")
	}
	if _, ok := reg.Get("wait_agent"); !ok {
		t.Fatal("wait_agent tool not registered")
	}
	if _, ok := reg.Get("get_agent_output"); !ok {
		t.Fatal("get_agent_output tool not registered")
	}
}

func TestRegisterToolsAddsGitMergeStatus(t *testing.T) {
	reg := tools.NewRegistry()
	cfg := &config.Config{}
	workDir := t.TempDir()

	_, _ = registerTools(reg, workDir, cfg, reactruntime.NewSession(), agent.YoloApproval(), nil, nil)
	if _, ok := reg.Get("git_merge_status"); !ok {
		t.Fatal("git_merge_status tool not registered")
	}
}

func TestRegisterToolsAddsGitBranchState(t *testing.T) {
	reg := tools.NewRegistry()
	cfg := &config.Config{}
	workDir := t.TempDir()

	_, _ = registerTools(reg, workDir, cfg, reactruntime.NewSession(), agent.YoloApproval(), nil, nil)
	if _, ok := reg.Get("git_branch_state"); !ok {
		t.Fatal("git_branch_state tool not registered")
	}
}

func TestRegisterToolsAddsCodexStyleEditingAndPlanningTools(t *testing.T) {
	reg := tools.NewRegistry()
	cfg := &config.Config{}
	workDir := t.TempDir()

	_, _ = registerTools(reg, workDir, cfg, reactruntime.NewSession(), agent.YoloApproval(), nil, nil)
	for _, name := range []string{"apply_patch", "update_plan", "enter_plan_mode", "exit_plan_mode", "ask_user_question", "tool_help", "view_image", "code_search", "lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("%s tool not registered", name)
		}
	}
}

func TestRegisterToolsAddsMCPResourceToolsWhenServersConfigured(t *testing.T) {
	oldFactory := newChatMCPManager
	defer func() { newChatMCPManager = oldFactory }()

	manager := mcp.NewManager()
	cfg := &config.Config{
		MCPServers: map[string]config.MCPServerConfig{
			"context7": {Type: "stdio", Command: []string{"ignored"}},
		},
	}
	manager.FreezeForTesting(cfg, mcp.Snapshot{
		Tools: []mcp.Tool{{
			ServerName:  "context7",
			Name:        "resolve_library_id",
			Description: "Resolve docs library ids.",
		}},
	})
	newChatMCPManager = func() *mcp.Manager { return manager }

	reg := tools.NewRegistry()
	workDir := t.TempDir()

	_, _ = registerTools(reg, workDir, cfg, reactruntime.NewSession(), agent.YoloApproval(), nil, nil)
	for _, name := range []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource", "mcp__context7__resolve_library_id"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("%s tool not registered", name)
		}
	}
}

func TestRegisterReactDelegationToolsDoesNotUseLegacyRoleModelMapping(t *testing.T) {
	reg := tools.NewRegistry()
	cfg := &config.Config{}
	workDir := t.TempDir()
	approve := agent.YoloApproval()
	_, _ = registerTools(reg, workDir, cfg, reactruntime.NewSession(), approve, nil, nil)
	baseReg := reg.Filter(nil)

	var makeDriverCalls []string
	setup := &ChatSetup{
		Config:  cfg,
		WorkDir: workDir,
		Driver:  &kernelMockDriver{response: "spawned result"},
		MakeDriver: func(name string) llm.Driver {
			makeDriverCalls = append(makeDriverCalls, name)
			return &kernelMockDriver{response: "spawned result"}
		},
	}

	registerReactDelegationTools(reg, setup, baseReg, approve, nil, nil)

	spawnTool, ok := reg.Get("spawn_agent")
	if !ok {
		t.Fatal("spawn_agent tool not registered")
	}
	waitTool, ok := reg.Get("wait_agent")
	if !ok {
		t.Fatal("wait_agent tool not registered")
	}

	rawSpawn, err := spawnTool.Execute(context.Background(), map[string]any{
		"task_description": "inspect repo",
		"role":             "explorer",
	})
	if err != nil {
		t.Fatal(err)
	}

	var spawnPayload map[string]any
	if err := json.Unmarshal([]byte(rawSpawn), &spawnPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := spawnPayload["id"].(string)
	if id == "" {
		t.Fatal("spawn id missing")
	}

	if _, err := waitTool.Execute(context.Background(), map[string]any{
		"id":              id,
		"timeout_seconds": 1.0,
	}); err != nil {
		t.Fatal(err)
	}

	if len(makeDriverCalls) != 0 {
		t.Fatalf("react delegation should not consult legacy role-model mapping, got makeDriver calls %v", makeDriverCalls)
	}
}

func TestRegisterReactDelegationToolsStreamsSubAgentEvents(t *testing.T) {
	reg := tools.NewRegistry()
	cfg := &config.Config{}
	workDir := t.TempDir()
	approve := agent.YoloApproval()
	_, _ = registerTools(reg, workDir, cfg, reactruntime.NewSession(), approve, nil, nil)
	baseReg := reg.Filter(nil)
	events := make(chan llm.Event, 16)
	renderer := agent.NewEventRenderer(events)
	setup := &ChatSetup{
		Config:  cfg,
		WorkDir: workDir,
		Driver:  &kernelNativeTextDriver{response: "sub-agent result"},
	}

	registerReactDelegationTools(reg, setup, baseReg, approve, renderer, nil)
	spawnTool, ok := reg.Get("spawn_agent")
	if !ok {
		t.Fatal("spawn_agent tool not registered")
	}
	waitTool, ok := reg.Get("wait_agent")
	if !ok {
		t.Fatal("wait_agent tool not registered")
	}
	rawSpawn, err := spawnTool.Execute(context.Background(), map[string]any{
		"task_description": "say hello",
		"role":             "code researcher",
	})
	if err != nil {
		t.Fatal(err)
	}
	var spawnPayload map[string]any
	if err := json.Unmarshal([]byte(rawSpawn), &spawnPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := spawnPayload["id"].(string)
	if id == "" {
		t.Fatal("spawn id missing")
	}
	waitResult, err := waitTool.Execute(context.Background(), map[string]any{"id": id, "timeout_seconds": 1.0})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	var queued []llm.Event
	for len(events) > 0 {
		ev := <-events
		queued = append(queued, ev)
		if ev.SubAgent == "code researcher" && ev.Kind == llm.EventToken && strings.Contains(ev.Text, "sub-agent result") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected sub-agent token event, wait result = %s, queued events = %#v", waitResult, queued)
	}
}

func TestRegisterReactDelegationToolsRestrictsCodeReviewerToReadOnlyTools(t *testing.T) {
	reg := tools.NewRegistry()
	cfg := &config.Config{}
	workDir := t.TempDir()
	approve := agent.YoloApproval()
	_, _ = registerTools(reg, workDir, cfg, reactruntime.NewSession(), approve, nil, nil)
	baseReg := reg.Filter(nil)
	driver := &captureToolDefsDriver{response: "review findings"}
	setup := &ChatSetup{
		Config:  cfg,
		WorkDir: workDir,
		Driver:  driver,
	}

	registerReactDelegationTools(reg, setup, baseReg, approve, nil, nil)
	spawnTool, ok := reg.Get("spawn_agent")
	if !ok {
		t.Fatal("spawn_agent tool not registered")
	}
	waitTool, ok := reg.Get("wait_agent")
	if !ok {
		t.Fatal("wait_agent tool not registered")
	}
	rawSpawn, err := spawnTool.Execute(context.Background(), map[string]any{
		"task_description": "review this implementation and fix it if anything is obvious",
		"role":             "code-reviewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	var spawnPayload map[string]any
	if err := json.Unmarshal([]byte(rawSpawn), &spawnPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := spawnPayload["id"].(string)
	if id == "" {
		t.Fatal("spawn id missing")
	}
	if _, err := waitTool.Execute(context.Background(), map[string]any{"id": id, "timeout_seconds": 1.0}); err != nil {
		t.Fatal(err)
	}

	for _, forbidden := range []string{"apply_patch", "edit_file", "write_file", "artifact_write"} {
		if slices.Contains(driver.toolNames, forbidden) {
			t.Fatalf("code-reviewer saw mutating tool %q in %#v", forbidden, driver.toolNames)
		}
	}
}

func TestRegisterReactDelegationToolsGivesSanitizedAuditReadOnlyTools(t *testing.T) {
	reg := tools.NewRegistry()
	cfg := &config.Config{}
	workDir := t.TempDir()
	approve := agent.YoloApproval()
	_, _ = registerTools(reg, workDir, cfg, reactruntime.NewSession(), approve, nil, nil)
	baseReg := reg.Filter(nil)
	driver := &captureToolDefsDriver{response: "audit findings"}
	setup := &ChatSetup{
		Config:  cfg,
		WorkDir: workDir,
		Driver:  driver,
	}

	registerReactDelegationTools(reg, setup, baseReg, approve, nil, nil)
	spawnTool, ok := reg.Get("spawn_agent")
	if !ok {
		t.Fatal("spawn_agent tool not registered")
	}
	waitTool, ok := reg.Get("wait_agent")
	if !ok {
		t.Fatal("wait_agent tool not registered")
	}
	rawSpawn, err := spawnTool.Execute(context.Background(), map[string]any{
		"task_description": "Use repo-auditor to audit this disposable repo. The child agent must only inspect and return findings. After wait_agent completes, the parent agent must write docs/reports/live-agent-write.md with the audit findings. Do not commit.",
		"role":             "repo-auditor",
	})
	if err != nil {
		t.Fatal(err)
	}
	var spawnPayload map[string]any
	if err := json.Unmarshal([]byte(rawSpawn), &spawnPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := spawnPayload["id"].(string)
	if id == "" {
		t.Fatal("spawn id missing")
	}
	if _, err := waitTool.Execute(context.Background(), map[string]any{"id": id, "timeout_seconds": 1.0}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"read_file", "list_dir", "glob", "git_status"} {
		if !slices.Contains(driver.toolNames, want) {
			t.Fatalf("repo-auditor tools = %#v, want %s", driver.toolNames, want)
		}
	}
	for _, forbidden := range []string{"apply_patch", "edit_file", "write_file", "run_command", "git_commit"} {
		if slices.Contains(driver.toolNames, forbidden) {
			t.Fatalf("repo-auditor saw mutating tool %q in %#v", forbidden, driver.toolNames)
		}
	}
	for _, want := range []string{
		"You have repository access through these native tools",
		"git_status",
		"Do not tell the user to run git commands",
		"forge_handoff",
		"parent/orchestrator owns writes, repairs, verification, commits, and user questions",
	} {
		if !messagesContain(driver.messages, want) {
			t.Fatalf("repo-auditor prompt missing %q: %#v", want, driver.messages)
		}
	}
}

func TestRegisterDelegationToolsStripsGitMutationFromChildren(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, session, approve, nil, nil)

	childReg := childRegistryForRole(reg, "repo-auditor", session.Snapshot())
	for _, forbidden := range []string{"write_file", "edit_file", "apply_patch", "run_command", "git_commit", "git_push"} {
		if _, ok := childReg.Get(forbidden); ok {
			t.Fatalf("child registry includes forbidden tool %s", forbidden)
		}
	}
}

func TestChildRegistryForResearchRolesStripsMutationTools(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, session, approve, nil, nil)

	for _, role := range []string{"research", "audit", "explore", "review"} {
		t.Run(role, func(t *testing.T) {
			childReg := childRegistryForRole(reg, role, session.Snapshot())
			for _, forbidden := range []string{"write_file", "edit_file", "apply_patch", "run_command", "git_commit", "git_push"} {
				if _, ok := childReg.Get(forbidden); ok {
					t.Fatalf("child registry includes forbidden tool %s", forbidden)
				}
			}
		})
	}
}

func TestChildRegistryForImplementerRolesRequiresAllowedPathsForWrites(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, reactruntime.NewSession(), approve, nil, nil)

	for _, tc := range []struct {
		name       string
		snap       reactruntime.SessionSnapshot
		wantWrites bool
	}{
		{name: "empty allowed paths", snap: reactruntime.SessionSnapshot{SideEffectIntent: &reactruntime.SideEffectIntent{}}, wantWrites: false},
		{name: "non-empty allowed paths", snap: reactruntime.SessionSnapshot{SideEffectIntent: &reactruntime.SideEffectIntent{AllowedPaths: []string{"docs/report.md"}}}, wantWrites: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			childReg := childRegistryForRole(reg, "implementer", tc.snap)
			for _, name := range []string{"write_file", "edit_file", "apply_patch", "artifact_write"} {
				_, ok := childReg.Get(name)
				if ok != tc.wantWrites {
					t.Fatalf("tool %s present = %v, want %v", name, ok, tc.wantWrites)
				}
			}
			for _, forbidden := range []string{"run_command", "git_commit", "git_push"} {
				if _, ok := childReg.Get(forbidden); ok {
					t.Fatalf("implementer child registry includes parent-owned tool %s", forbidden)
				}
			}
		})
	}
}

func TestChildRegistryForImplementerRolesScopesWriteToolsToAllowedPaths(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"docs/allowed-edit.md":     "old allowed",
		"docs/disallowed-edit.md":  "old disallowed",
		"docs/disallowed-write.md": "original",
	} {
		if err := os.WriteFile(filepath.Join(workDir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, workDir, cfg, reactruntime.NewSession(), approve, nil, nil)
	childReg := childRegistryForRole(reg, "implementer", reactruntime.SessionSnapshot{SideEffectIntent: &reactruntime.SideEffectIntent{
		AllowedPaths:  []string{"docs/allowed-write.md", "docs/allowed-edit.md", "docs/allowed-patch.md"},
		ArtifactPaths: []string{"docs/allowed-artifact.md"},
		WorkspaceRoot: workDir,
	}})

	for _, tc := range []struct {
		name           string
		tool           string
		allowedArgs    map[string]any
		disallowedArgs map[string]any
		allowedPath    string
		disallowedPath string
	}{
		{
			name:           "write_file",
			tool:           "write_file",
			allowedArgs:    map[string]any{"path": "docs/allowed-write.md", "content": "allowed"},
			disallowedArgs: map[string]any{"path": "docs/disallowed-write.md", "content": "blocked"},
			allowedPath:    "docs/allowed-write.md",
			disallowedPath: "docs/disallowed-write.md",
		},
		{
			name:           "edit_file",
			tool:           "edit_file",
			allowedArgs:    map[string]any{"path": "docs/allowed-edit.md", "old_text": "old allowed", "new_text": "new allowed"},
			disallowedArgs: map[string]any{"path": "docs/disallowed-edit.md", "old_text": "old disallowed", "new_text": "blocked"},
			allowedPath:    "docs/allowed-edit.md",
			disallowedPath: "docs/disallowed-edit.md",
		},
		{
			name:           "artifact_write",
			tool:           "artifact_write",
			allowedArgs:    map[string]any{"path": "docs/allowed-artifact.md", "content": "allowed"},
			disallowedArgs: map[string]any{"path": "docs/disallowed-artifact.md", "content": "blocked"},
			allowedPath:    "docs/allowed-artifact.md",
			disallowedPath: "docs/disallowed-artifact.md",
		},
		{
			name: "apply_patch",
			tool: "apply_patch",
			allowedArgs: map[string]any{"patch": `diff --git a/docs/allowed-patch.md b/docs/allowed-patch.md
new file mode 100644
--- /dev/null
+++ b/docs/allowed-patch.md
@@ -0,0 +1 @@
+allowed`},
			disallowedArgs: map[string]any{"patch": `diff --git a/docs/disallowed-patch.md b/docs/disallowed-patch.md
new file mode 100644
--- /dev/null
+++ b/docs/disallowed-patch.md
@@ -0,0 +1 @@
+blocked`},
			allowedPath:    "docs/allowed-patch.md",
			disallowedPath: "docs/disallowed-patch.md",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool, ok := childReg.Get(tc.tool)
			if !ok {
				t.Fatalf("%s missing from implementer child registry", tc.tool)
			}
			if _, err := tool.Execute(context.Background(), tc.allowedArgs); err != nil {
				t.Fatalf("allowed %s failed: %v", tc.tool, err)
			}
			result, err := tool.Execute(context.Background(), tc.disallowedArgs)
			if err != nil {
				t.Fatalf("disallowed %s returned error: %v", tc.tool, err)
			}
			if !strings.Contains(strings.ToLower(result), "blocked") {
				t.Fatalf("disallowed %s result = %q, want blocked", tc.tool, result)
			}
			if _, err := os.Stat(filepath.Join(workDir, tc.allowedPath)); err != nil {
				t.Fatalf("allowed path was not written: %v", err)
			}
			if data, err := os.ReadFile(filepath.Join(workDir, tc.disallowedPath)); err == nil && strings.Contains(string(data), "blocked") {
				t.Fatalf("disallowed path was mutated: %s", data)
			} else if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
}

func TestChildRegistryCompoundReadOnlyRolesWinOverImplementerTokens(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, reactruntime.NewSession(), approve, nil, nil)
	snap := reactruntime.SessionSnapshot{SideEffectIntent: &reactruntime.SideEffectIntent{AllowedPaths: []string{"docs/report.md"}}}

	for _, role := range []string{"implementation-review", "developer-review", "research-worker"} {
		t.Run(role, func(t *testing.T) {
			childReg := childRegistryForRole(reg, role, snap)
			for _, forbidden := range []string{"write_file", "edit_file", "apply_patch", "run_command", "git_commit", "git_push"} {
				if _, ok := childReg.Get(forbidden); ok {
					t.Fatalf("child registry includes forbidden tool %s", forbidden)
				}
			}
		})
	}
}

func TestChildAgentToolAccessPromptIncludesChildMutationBoundary(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "read_file"})
	prompt := childAgentToolAccessPrompt(reg)
	want := "Child agents must not commit or push. Return findings and proposed artifact content. Parent/orchestrator owns write, commit, push, and verification gates."
	if !strings.Contains(prompt, want) {
		t.Fatalf("child tool access prompt missing %q:\n%s", want, prompt)
	}
}

func messagesContain(messages []llm.Message, needle string) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}

// ── Integration hardening (Task 10) ──────────────────────────────────────────

// TestLightweightChatPathStaysDirect verifies that a simple conversational
// question does not accumulate task state, plan mode, or complex overlays.
func TestLightweightChatPathStaysDirect(t *testing.T) {
	reactRunner := &stubChatTurnRunner{}
	input := "what time is it"
	if err := runChatTurn(context.Background(), reactRunner, chatstate.ChatUserInput{IsInput: true, Text: input}); err != nil {
		t.Fatal(err)
	}
	// Simple question should not seed task state.
	if reactRunner.taskState != nil {
		t.Fatalf("expected no task state for simple question, got %+v", *reactRunner.taskState)
	}
}

// TestBehaviorStackDoesNotCorruptBasePromptAssembly verifies that memory summaries,
// hook overlays, and task state can all coexist in one session without corrupting
// the base system prompt or each other.
func TestBehaviorStackDoesNotCorruptBasePromptAssembly(t *testing.T) {
	session := reactruntime.NewSession()

	// Inject a memory summary.
	session.SetMemorySummary("important context: project uses ruff for linting")

	// Inject a hook overlay.
	session.SetHookOverlay(reactruntime.HookOverlay{
		Key:        "test_overlay",
		Content:    "test hook overlay content",
		Priority:   reactruntime.HookPriorityNormal,
		Provenance: "test",
	})

	// Set task state.
	session.SetTaskState(reactruntime.TaskState{
		Objective: "refactor the auth module",
		Operation: "implement",
	})

	msgs := session.Messages("base system prompt")

	// Base system prompt must be first.
	if len(msgs) == 0 || msgs[0].Role != "system" || msgs[0].Content != "base system prompt" {
		t.Fatalf("base system prompt not first, got: %+v", msgs)
	}

	// Verify memory, overlay, and task state each appear somewhere.
	var hasMemory, hasOverlay, hasTask bool
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "Memory summary") {
			hasMemory = true
		}
		if strings.Contains(msg.Content, "test hook overlay content") {
			hasOverlay = true
		}
		if strings.Contains(msg.Content, "refactor the auth module") {
			hasTask = true
		}
	}
	if !hasMemory {
		t.Error("memory summary not found in messages")
	}
	if !hasOverlay {
		t.Error("hook overlay not found in messages")
	}
	if !hasTask {
		t.Error("task state not found in messages")
	}
}

// TestSuggestedSkillNudgeReachesNotifyCallback verifies that when a skill is
// loaded and matches the input heuristic, suggestedSkillNudge returns a non-empty
// nudge that could be forwarded to tui.NotifyNudge.
func TestSuggestedSkillNudgeReachesNotifyCallback(t *testing.T) {
	// "brainstorming" is auto-detected when input contains "plan", "design", etc.
	loaded := []skills.Skill{
		{Name: "brainstorming", Description: "structured planning"},
	}
	state := chatstate.New()
	nudge := suggestedSkillNudge("make a plan for this feature", loaded, state)
	if nudge == "" {
		t.Fatal("expected non-empty nudge for matching skill")
	}
	if !strings.Contains(nudge, "brainstorming") {
		t.Fatalf("nudge should mention the skill name, got %q", nudge)
	}
}

// TestMemoryAndSkillOverlaysCoexistInPromptAssembly verifies that both a memory
// summary overlay and a skill hook overlay can appear together in the assembled
// messages without one displacing the other.
func TestMemoryAndSkillOverlaysCoexistInPromptAssembly(t *testing.T) {
	session := reactruntime.NewSession()
	session.SetMemorySummary("last session: worked on auth module")
	session.SetTaskState(reactruntime.TaskState{
		Objective: "inspect the auth module",
		Operation: "inspect",
	})
	session.SetHookOverlay(reactruntime.HookOverlay{
		Key:        "suggested_skill",
		Content:    "suggested skill: /code-review (change set looks reviewable)",
		Priority:   reactruntime.HookPriorityNormal,
		Provenance: "runtime",
	})

	msgs := session.Messages("system")

	var memCount, skillCount int
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "Memory summary") {
			memCount++
		}
		if strings.Contains(msg.Content, "suggested skill") {
			skillCount++
		}
	}
	if memCount != 1 {
		t.Errorf("expected exactly 1 memory message, got %d", memCount)
	}
	if skillCount != 1 {
		t.Errorf("expected exactly 1 skill overlay message, got %d", skillCount)
	}
}

// TestTuiSelectNudgeIntegratesWithRuntime verifies that the tui.SelectNudge
// function produces expected nudge kinds for the operation types that
// detectTaskStateFromInput emits, so the two subsystems stay in sync.

type stubChatTurnRunner struct {
	calls        int
	input        string
	err          error
	lastResponse string
	skillName    string
	skillBody    string
	taskState    *reactruntime.TaskState
	queued       []string
	interrupted  bool
	parts        []llm.MessageContentPart
}

func (s *stubChatTurnRunner) CompactHistory(int) bool { return false }

func (s *stubChatTurnRunner) CompactionStatus() string { return "no compacted turns" }

func (s *stubChatTurnRunner) Run(_ context.Context, input string) error {
	s.calls++
	s.input = input
	return s.err
}

func (s *stubChatTurnRunner) RunWithParts(_ context.Context, input string, parts []llm.MessageContentPart) error {
	s.calls++
	s.input = input
	s.parts = parts
	return s.err
}

func (s *stubChatTurnRunner) EmitResponse(text string) {
	s.lastResponse = text
}

func (s *stubChatTurnRunner) AppendSkillContext(name, body string) {
	s.skillName = name
	s.skillBody = body
}

func (s *stubChatTurnRunner) SetTaskState(state reactruntime.TaskState) {
	if strings.TrimSpace(state.Objective) == "" && strings.TrimSpace(state.RequiredVerification) == "" {
		s.taskState = nil
		return
	}
	s.taskState = &state
}

func (s *stubChatTurnRunner) TaskState() *reactruntime.TaskState {
	if s == nil || s.taskState == nil {
		return nil
	}
	cloned := *s.taskState
	return &cloned
}

func (s *stubChatTurnRunner) QueuePendingInput(text string) {
	s.queued = append(s.queued, text)
}

func (s *stubChatTurnRunner) DiscardPendingInput() []string {
	discarded := s.queued
	s.queued = nil
	return discarded
}

func (s *stubChatTurnRunner) MarkInterrupted() {
	s.interrupted = true
}

type stubChatSessionControl struct {
	driver          llm.Driver
	cleared         bool
	lastUserMessage string
	lastSkillName   string
	lastSkillBody   string
	lastResponse    string
	taskState       *reactruntime.TaskState
	compactKeep     int
	compactChanged  bool
	compactStatus   string
}

func (s *stubChatSessionControl) SetDriver(driver llm.Driver) {
	s.driver = driver
}

func (s *stubChatSessionControl) ClearHistory() {
	s.cleared = true
}

func (s *stubChatSessionControl) AppendUserMessage(text string) {
	s.lastUserMessage = text
}

func (s *stubChatSessionControl) AppendSkillContext(name, body string) {
	s.lastSkillName = name
	s.lastSkillBody = body
}

func (s *stubChatSessionControl) EmitResponse(text string) {
	s.lastResponse = text
}

func (s *stubChatSessionControl) SetTaskState(state reactruntime.TaskState) {
	if strings.TrimSpace(state.Objective) == "" && strings.TrimSpace(state.RequiredVerification) == "" {
		s.taskState = nil
		return
	}
	s.taskState = &state
}

func (s *stubChatSessionControl) CompactHistory(keep int) bool {
	s.compactKeep = keep
	return s.compactChanged
}

func (s *stubChatSessionControl) CompactionStatus() string {
	return s.compactStatus
}

func TestNewChildAgentRegistryCreatesToolsWithCorrectWorkDir(t *testing.T) {
	parentDir := t.TempDir()
	childDir := t.TempDir()
	cfg := &config.Config{}
	approve := agent.YoloApproval()

	// Write a marker file only in the child directory
	if err := os.WriteFile(filepath.Join(childDir, "marker.txt"), []byte("child-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	parentReg := tools.NewRegistry()
	_, _ = registerTools(parentReg, parentDir, cfg, reactruntime.NewSession(), approve, nil, nil)
	baseReg := parentReg.Filter(nil)

	setup := &ChatSetup{Config: cfg, WorkDir: parentDir}
	childReg := newChildAgentRegistry(childDir, nil, baseReg, setup, approve)

	readTool, ok := childReg.Get("read_file")
	if !ok {
		t.Fatal("read_file missing from child registry")
	}
	result, err := readTool.Execute(context.Background(), map[string]any{
		"path": filepath.Join(childDir, "marker.txt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "child-content") {
		t.Fatalf("read_file from child dir = %q, want child-content", result)
	}

	// Verify git tools also point at child dir
	gitTool, ok := childReg.Get("git_status")
	if !ok {
		t.Fatal("git_status missing from child registry")
	}
	// Running git_status in the childDir (which has no git repo) should produce an error,
	// but it should NOT return the parent dir's git status
	result, err = gitTool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "fatal: not a git repository") {
		return // correct: child dir is not a git repo
	}
	if strings.Contains(result, "MarkerFile") || strings.Contains(result, "modified") || strings.Contains(result, "chat_test") {
		t.Fatalf("git_status appears to have run in parent dir: %q", result)
	}
}

func TestChildRegistryToolsUseAllowedToolsFilter(t *testing.T) {
	parentDir := t.TempDir()
	childDir := t.TempDir()
	cfg := &config.Config{}
	approve := agent.YoloApproval()

	parentReg := tools.NewRegistry()
	_, _ = registerTools(parentReg, parentDir, cfg, reactruntime.NewSession(), approve, nil, nil)
	baseReg := parentReg.Filter(nil)

	setup := &ChatSetup{Config: cfg, WorkDir: parentDir}
	childReg := newChildAgentRegistry(childDir, []string{"read_file", "tool_help", "think"}, baseReg, setup, approve)

	for _, name := range []string{"read_file", "tool_help", "think"} {
		if _, ok := childReg.Get(name); !ok {
			t.Fatalf("allowed tool %q missing from child registry", name)
		}
	}
	if _, ok := childReg.Get("git_status"); ok {
		t.Fatal("git_status should not be present when not in allowed tools")
	}
}

type kernelMockDriver struct {
	response  string
	responses []string
	callIdx   int
}

func (d *kernelMockDriver) Name() string { return "kernel-mock" }

func (d *kernelMockDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	if d.callIdx < len(d.responses) {
		out <- llm.Token{Text: d.responses[d.callIdx]}
		d.callIdx++
		return nil
	}
	out <- llm.Token{Text: d.response}
	return nil
}

type kernelNativeTextDriver struct {
	response string
}

func (d *kernelNativeTextDriver) Name() string { return "kernel-native-text" }

func (d *kernelNativeTextDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	out <- llm.Token{Text: d.response}
	return nil
}

func (d *kernelNativeTextDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	out <- llm.Token{Text: d.response}
	return nil
}

type captureToolDefsDriver struct {
	response  string
	toolNames []string
	messages  []llm.Message
}

func (d *captureToolDefsDriver) Name() string { return "capture-tool-defs" }

func (d *captureToolDefsDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	out <- llm.Token{Text: d.response}
	return nil
}

func (d *captureToolDefsDriver) StreamWithTools(_ context.Context, messages []llm.Message, defs []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.messages = append([]llm.Message(nil), messages...)
	d.toolNames = d.toolNames[:0]
	for _, def := range defs {
		d.toolNames = append(d.toolNames, def.Name)
	}
	out <- llm.Token{Text: d.response}
	return nil
}

func captureRuntimeStdout(t *testing.T, fn func()) string {
	t.Helper()
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = prev }()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	_ = r.Close()
	return buf.String()
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
