package runtime

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/agent"
	"forge/internal/agent/tools"
	"forge/internal/auth"
	"forge/internal/chatstate"
	"forge/internal/config"
	"forge/internal/harness"
	"forge/internal/llm"
)

func TestResolveModelExactAndIndexed(t *testing.T) {
	models := []string{"alpha/model-a", "beta/model-b", "gamma/model-c"}
	if got := ResolveModel(models, "2"); got != "beta/model-b" {
		t.Fatalf("expected indexed model, got %q", got)
	}
	if got := ResolveModel(models, "gamma/model-c"); got != "gamma/model-c" {
		t.Fatalf("expected exact model, got %q", got)
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

func TestHandleChatSlashCommandExpandIsUnknown(t *testing.T) {
	var buf bytes.Buffer
	renderer := agent.NewRenderer(&buf, 80, false)
	a := agent.NewAgent(nil, tools.NewRegistry(), agent.YoloApproval(), t.TempDir(), 4, renderer, nil, chatstate.New())
	setup := &ChatSetup{}

	if handled := handleChatSlashCommand("/expand", renderer, a, setup); !handled {
		t.Fatal("expected slash command to be handled")
	}
	if !strings.Contains(buf.String(), "unknown command: /expand") {
		t.Fatalf("expected unknown command output, got %q", buf.String())
	}
}

func TestUseHarnessKernelRuntimeReadsEnv(t *testing.T) {
	t.Setenv("FORGE_CHAT_RUNTIME", "")
	if !useHarnessKernelRuntime() {
		t.Fatal("kernel runtime should be enabled by default")
	}

	t.Setenv("FORGE_CHAT_RUNTIME", "legacy")
	if useHarnessKernelRuntime() {
		t.Fatal("kernel runtime should be disabled when env requests legacy mode")
	}

	t.Setenv("FORGE_CHAT_RUNTIME", "kernel")
	if !useHarnessKernelRuntime() {
		t.Fatal("kernel runtime should be enabled when env requests it")
	}
}

func TestRunChatTurnUsesKernelWhenProvided(t *testing.T) {
	kernel := harness.NewRunner(harness.RunnerConfig{
		Session: harness.NewSession(),
		Trace:   harness.NewRecorder(),
		Local:   stubHarnessLocalExecutor{response: "kernel path"},
	})

	if err := runChatTurn(context.Background(), nil, kernel, "describe this directory"); err != nil {
		t.Fatal(err)
	}

	trace := kernel.Trace()
	if len(trace) == 0 {
		t.Fatal("expected kernel trace records")
	}
	if trace[1].Family != harness.FamilyInspect {
		t.Fatalf("unexpected classification trace: %#v", trace)
	}
}

func TestRunChatTurnKernelPathAvoidsDelegationMarkers(t *testing.T) {
	events := make(chan llm.Event, 16)
	renderer := agent.NewEventRenderer(events)
	driver := &kernelMockDriver{response: "Directory contains cmd and internal."}
	a := agent.NewAgent(driver, tools.NewRegistry(), agent.YoloApproval(), t.TempDir(), 4, renderer, nil, chatstate.New())
	kernel := harness.NewRunner(harness.RunnerConfig{
		Session: harness.NewSession(),
		Trace:   harness.NewRecorder(),
		Local:   harness.AgentExecutor{Agent: a},
	})

	if err := runChatTurn(context.Background(), a, kernel, "describe this directory"); err != nil {
		t.Fatal(err)
	}

	for {
		select {
		case ev := <-events:
			if ev.Kind == llm.EventToolCall && ev.Agent == "runtime" && strings.Contains(strings.ToLower(ev.Text), "delegating") {
				t.Fatalf("unexpected delegation marker: %#v", ev)
			}
		default:
			return
		}
	}
}

type stubHarnessLocalExecutor struct {
	response string
}

func (s stubHarnessLocalExecutor) Execute(_ context.Context, _ harness.UserTurn, class harness.Classification) (harness.Observation, error) {
	return harness.Observation{
		Status:   harness.ObservationComplete,
		Response: s.response,
		Summary:  s.response,
		TopicKey: class.TopicKey,
	}, nil
}

type kernelMockDriver struct {
	response string
}

func (d *kernelMockDriver) Name() string { return "kernel-mock" }

func (d *kernelMockDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
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
