package runtime

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"forge/internal/agent"
	"forge/internal/agent/tools"
	"forge/internal/auth"
	"forge/internal/bootstrap"
	"forge/internal/chatstate"
	"forge/internal/codexusage"
	"forge/internal/config"
	"forge/internal/copilot"
	"forge/internal/llm"
	"forge/internal/modelcatalog"
	reactruntime "forge/internal/react"
	reacttools "forge/internal/react/tools"
	"forge/internal/skills"
	"forge/internal/tui"
)

var (
	loadChatConfig    = bootstrap.LoadConfig
	loadChatTokens    = bootstrap.LoadTokens
	saveLastChatModel = config.SaveChatLastModel
	defaultConfigPath = config.DefaultPath
	runChatLiveUI     = tui.RunChatLive
)

type chatRuntimeMode string

const (
	chatRuntimeReact chatRuntimeMode = "react"
)

type ChatSetup struct {
	Config     *config.Config
	ChatModel  string
	WorkDir    string
	Driver     llm.Driver
	Yolo       bool
	Available  []string
	Providers  []tui.ProviderOption
	MakeDriver func(string) llm.Driver
	DebugLog   string
	debugRec   *chatDebugRecorder
}

func BuildChatSetup(cfg *config.Config, tokens any, modelOverride, workDir string, yolo bool) (*ChatSetup, error) {
	_ = tokens
	wd := "."
	if workDir != "" {
		wd = workDir
	}
	absWd, err := filepath.Abs(wd)
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}

	authTokens, _ := loadChatTokens()
	if authTokens == nil {
		authTokens = &auth.Tokens{}
	}
	available := bootstrap.AvailableModels(cfg, authTokens)
	providers := providerOptionsFromBootstrap(bootstrap.SupportedProviderBackends(cfg, authTokens))
	chatModel := cfg.ChatModel()
	if modelOverride != "" {
		chatModel = modelOverride
	} else if chatModel != "" && !ContainsModel(available, chatModel) {
		chatModel = ""
	}

	driverReg := llm.NewRegistry()
	makeChatDriver := func(modelName string) llm.Driver {
		effectiveCfg := cfg
		if latestCfg, err := loadChatConfig(); err == nil && latestCfg != nil {
			effectiveCfg = latestCfg
		}
		effectiveTokens := authTokens
		if latestTokens, err := loadChatTokens(); err == nil && latestTokens != nil {
			effectiveTokens = latestTokens
		}
		bootstrap.EnsureDriver(effectiveCfg, effectiveTokens, driverReg, modelName)
		d, err := driverReg.Lookup(modelName)
		if err != nil {
			return nil
		}
		return llm.NewRetryDriver(d,
			effectiveCfg.Retry.MaxAttempts,
			time.Duration(effectiveCfg.Retry.InitialWait)*time.Millisecond,
			time.Duration(effectiveCfg.Retry.MaxWait)*time.Millisecond,
			time.Duration(effectiveCfg.Retry.Timeout)*time.Second,
		)
	}

	var driver llm.Driver
	if strings.TrimSpace(chatModel) != "" {
		driver = makeChatDriver(chatModel)
		if driver == nil {
			return nil, fmt.Errorf("no API key found for model %q", chatModel)
		}
		persistChatLastModel(cfg, chatModel)
	}

	return &ChatSetup{
		Config:     cfg,
		ChatModel:  chatModel,
		WorkDir:    absWd,
		Driver:     driver,
		Yolo:       yolo,
		Available:  available,
		Providers:  providers,
		MakeDriver: makeChatDriver,
	}, nil
}

func persistChatLastModel(cfg *config.Config, model string) {
	if cfg == nil || strings.TrimSpace(model) == "" {
		return
	}
	cfg.Chat.LastModel = model
	_ = saveLastChatModel(defaultConfigPath(), model)
}

func refreshChatSetupState(setup *ChatSetup) (*config.Config, *auth.Tokens) {
	cfg := setup.Config
	if latestCfg, err := loadChatConfig(); err == nil && latestCfg != nil {
		setup.Config = latestCfg
		cfg = latestCfg
	}
	tokens, err := loadChatTokens()
	if err != nil || tokens == nil {
		tokens = &auth.Tokens{}
	}
	return cfg, tokens
}

func registerTools(reg *tools.Registry, workDir string, cfg *config.Config, approve tools.ApprovalFunc, forcePrompt ...tools.ApprovalFunc) *tools.PreviewRuntime {
	fp := approve
	if len(forcePrompt) > 0 {
		fp = forcePrompt[0]
	}
	previewRuntime := tools.NewPreviewRuntime(workDir, approve)
	reg.Register(tools.NewReadFile(workDir))
	reg.Register(tools.NewWriteFile(workDir, approve))
	reg.Register(tools.NewEditFile(workDir, approve))
	reg.Register(tools.NewArtifactWrite(previewRuntime))
	reg.Register(tools.NewArtifactRead(previewRuntime))
	reg.Register(tools.NewPreviewServerEnsure(previewRuntime))
	reg.Register(tools.NewPreviewServerStatus(previewRuntime))
	reg.Register(tools.NewListDir(workDir, cfg.Chat.IgnoreDirs))
	reg.Register(tools.NewSearch(workDir))
	reg.Register(tools.NewRunCommand(workDir, cfg.Chat.CommandTimeout, approve, fp))
	reg.Register(tools.NewThink())
	reg.Register(tools.NewGlob(workDir, cfg.Chat.IgnoreDirs))
	reg.Register(tools.NewGitStatus(workDir))
	reg.Register(tools.NewGitDiff(workDir))
	reg.Register(tools.NewGitLog(workDir))
	gitCommit := tools.NewGitCommit(workDir, approve)
	gitCommit.PromptVisibility = tools.PromptHidden
	reg.Register(gitCommit)
	webFetch := tools.NewWebFetch()
	webFetch.PromptVisibility = tools.PromptHidden
	reg.Register(webFetch)
	webSearch := tools.NewWebSearch()
	webSearch.PromptVisibility = tools.PromptHidden
	reg.Register(webSearch)
	reg.Register(tools.NewToolHelp(reg))
	return previewRuntime
}

func RunChatLive(setup *ChatSetup) {
	eventsCh := make(chan llm.Event, 256)
	renderCh := chan<- llm.Event(eventsCh)
	if setup != nil && setup.debugRec != nil {
		debugEvents := make(chan llm.Event, 256)
		renderCh = debugEvents
		go func() {
			for ev := range debugEvents {
				setup.debugRec.logEvent(ev)
				eventsCh <- ev
			}
		}()
	}
	evRenderer := agent.NewEventRenderer(renderCh)

	var approve tools.ApprovalFunc
	if setup.Yolo {
		approve = agent.YoloApproval()
	} else {
		approve = evRenderer.LiveApproval()
	}
	approve = reactruntime.NewApprovalGate(setup.WorkDir, reactruntime.LoadApprovalConfig(setup.Config), approve, func(text string) {
		evRenderer.Info(text)
	}).Approve

	reg := tools.NewRegistry()
	previewRuntime := registerTools(reg, setup.WorkDir, setup.Config, approve)
	if previewRuntime != nil {
		defer previewRuntime.Close()
	}
	baseReg := reg.Filter(nil)
	loadedSkills := skills.Load(setup.WorkDir)
	state := chatstate.New()
	reactRunner := reactruntime.NewRunner(reactruntime.Config{
		Driver:             setup.Driver,
		Tools:              reg,
		Renderer:           evRenderer,
		SystemPrompt:       func() string { return agent.BuildSystemPrompt(setup.WorkDir, reg, "") },
		NativeSystemPrompt: func() string { return agent.BuildNativeSystemPrompt(setup.WorkDir) },
		Session:            reactruntime.NewSession(),
		MaxSessionTurns:    20,
		Progress: func(text string) {
			evRenderer.Info(text)
		},
	})
	registerReactDelegationTools(reg, setup, baseReg, approve)

	inputCh := make(chan string, 1)
	doneCh := make(chan struct{}, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		var running bool
		var queue []string
		runOutcome := func(err error) string {
			if err != nil {
				bootstrap.ReportModelFailure(setup.ChatModel, err)
				evRenderer.Error(err.Error())
				return "__turn_failed__"
			}
			bootstrap.ReportModelSuccess(setup.ChatModel)
			return "__turn_done__"
		}
		startRun := func(msg string) {
			running = true
			if setup != nil && setup.debugRec != nil {
				setup.debugRec.logInput("user", msg)
			}
			go func(runMsg string) {
				err := runChatTurn(ctx, reactRunner, runMsg)
				inputCh <- runOutcome(err)
			}(msg)
		}
		for input := range inputCh {
			switch input {
			case "__approve_yes":
				if setup != nil && setup.debugRec != nil {
					setup.debugRec.logInput("control", input)
				}
				evRenderer.ResponseChan() <- true
			case "__approve_no":
				if setup != nil && setup.debugRec != nil {
					setup.debugRec.logInput("control", input)
				}
				evRenderer.ResponseChan() <- false
			case "__cancel_turn__":
				if setup != nil && setup.debugRec != nil {
					setup.debugRec.logInput("control", input)
				}
				if running {
					cancel()
					ctx, cancel = context.WithCancel(context.Background())
					queue = nil
					evRenderer.Info("turn canceled")
				}
			case "__turn_done__":
				evRenderer.TurnDone()
				running = false
				if len(queue) > 0 {
					next := queue[0]
					queue = queue[1:]
					evRenderer.Info(fmt.Sprintf("applying queued steering (%d remaining)", len(queue)))
					startRun(next)
				}
			case "__turn_failed__":
				running = false
				queue = nil
			default:
				if running {
					if setup != nil && setup.debugRec != nil {
						setup.debugRec.logInput("queued", input)
					}
					queue = append(queue, input)
					evRenderer.Info(fmt.Sprintf("queued steering: %s", input))
					continue
				}
				startRun(input)
			}
		}
	}()

	debugEnabled := setup.debugRec != nil
	surfaceKind := tui.ChatSurfaceDefault
	if debugEnabled {
		surfaceKind = tui.ChatSurfaceDebug
	}

	liveCfg := tui.ChatLiveConfig{
		Model:           setup.ChatModel,
		WorkDir:         setup.WorkDir,
		DebugLogPath:    setup.DebugLog,
		SurfaceKind:     surfaceKind,
		DebugEnabled:    debugEnabled,
		AvailableModels: setup.Available,
		Providers:       append([]tui.ProviderOption(nil), setup.Providers...),
		FetchLiveCopilotQuota: func(ctx context.Context) (*copilot.UserQuota, error) {
			if provider := bootstrap.ParseModelRef(setup.ChatModel).Provider; provider != "copilot" {
				return nil, nil
			}
			_, tokens := refreshChatSetupState(setup)
			if tokens == nil || strings.TrimSpace(tokens.CopilotToken) == "" {
				return nil, nil
			}
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			quota, err := copilot.FetchUserQuota(ctx, tokens.CopilotToken)
			if err != nil {
				return nil, err
			}
			return quota, nil
		},
		FetchCodexUsage: func(ctx context.Context) (*codexusage.Snapshot, error) {
			provider := bootstrap.ParseModelRef(setup.ChatModel).Provider
			if provider != "chatgpt" && provider != "openai" && provider != "codex" {
				return nil, nil
			}
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			snapshot, err := codexusage.FetchUsage(ctx)
			if err != nil {
				return nil, err
			}
			return snapshot, nil
		},
		ModelInfo: func(model string) *modelcatalog.ModelInfo {
			ref := bootstrap.ParseModelRef(model)
			return modelcatalog.Lookup(ref.Provider, ref.Model)
		},
		DescribeModel: func(model string) string {
			cfg, authTokens := refreshChatSetupState(setup)
			return bootstrap.ModelDisplayLabel(cfg, authTokens, model)
		},
		RequestMode: func() string {
			if reporter, ok := setup.Driver.(llm.RequestModeReporter); ok {
				return reporter.LastRequestMode()
			}
			return ""
		},
		RefreshModels: func() []string {
			cfg, authTokens := refreshChatSetupState(setup)
			setup.Available = bootstrap.AvailableModels(cfg, authTokens)
			return append([]string(nil), setup.Available...)
		},
		ProbeModels: func(currentModel string, available []string) []string {
			cfg, authTokens := refreshChatSetupState(setup)
			if len(available) == 0 {
				available = append([]string(nil), setup.Available...)
			}
			updated := bootstrap.ProbeProviderModels(cfg, authTokens, currentModel, available)
			setup.Available = append([]string(nil), updated...)
			return updated
		},
		RefreshProviders: func() []tui.ProviderOption {
			cfg, authTokens := refreshChatSetupState(setup)
			setup.Providers = providerOptionsFromBootstrap(bootstrap.SupportedProviderBackends(cfg, authTokens))
			return append([]tui.ProviderOption(nil), setup.Providers...)
		},
		SwitchModel: func(name string) (string, error) {
			refreshChatSetupState(setup)
			d := setup.MakeDriver(name)
			if d == nil {
				return "", fmt.Errorf("no API key found for model %q", name)
			}
			setup.ChatModel = name
			setup.Driver = d
			if reactRunner != nil {
				reactRunner.SetDriver(setup.Driver)
			}
			persistChatLastModel(setup.Config, name)
			return name, nil
		},
		ClearHistory: func() {
			state.Clear()
			if reactRunner != nil {
				reactRunner.ClearHistory()
			}
		},
		ApprovalCh:      evRenderer.ApprovalChan(),
		ResponseCh:      evRenderer.ResponseChan(),
		Skills:          loadedSkills,
		State:           state,
		CopilotClientID: setup.Config.CopilotClientID(),
	}
	runChatLiveUI(eventsCh, liveCfg, inputCh, doneCh)
}

func providerOptionsFromBootstrap(backends []bootstrap.ProviderBackend) []tui.ProviderOption {
	out := make([]tui.ProviderOption, 0, len(backends))
	for _, backend := range backends {
		out = append(out, tui.ProviderOption{
			ID:           backend.ID,
			Label:        backend.Label,
			Status:       backend.Status,
			DefaultModel: backend.DefaultModel,
		})
	}
	return out
}

func RunChatConsole(setup *ChatSetup) {
	var approve tools.ApprovalFunc
	if setup.Yolo {
		approve = agent.YoloApproval()
	} else {
		approve = agent.InteractiveApproval(os.Stdin, os.Stdout)
	}
	approve = reactruntime.NewApprovalGate(setup.WorkDir, reactruntime.LoadApprovalConfig(setup.Config), approve, func(text string) {
		_, _ = fmt.Fprintln(os.Stdout, text)
	}).Approve

	reg := tools.NewRegistry()
	forcePromptApprove := approve
	previewRuntime := registerTools(reg, setup.WorkDir, setup.Config, approve, forcePromptApprove)
	if previewRuntime != nil {
		defer previewRuntime.Close()
	}
	baseReg := reg.Filter(nil)
	loadedSkills := skills.Load(setup.WorkDir)

	renderer := agent.NewRenderer(os.Stdout, 80, true)
	state := chatstate.New()
	reactRunner := reactruntime.NewRunner(reactruntime.Config{
		Driver:             setup.Driver,
		Tools:              reg,
		Renderer:           renderer,
		SystemPrompt:       func() string { return agent.BuildSystemPrompt(setup.WorkDir, reg, "") },
		NativeSystemPrompt: func() string { return agent.BuildNativeSystemPrompt(setup.WorkDir) },
		Session:            reactruntime.NewSession(),
		MaxSessionTurns:    20,
		Progress: func(text string) {
			renderer.Info(text)
		},
	})
	registerReactDelegationTools(reg, setup, baseReg, approve)

	fmt.Printf("forge (%s) — %s\n", setup.ChatModel, setup.WorkDir)
	fmt.Println("type your request, or /help for commands")
	fmt.Println()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		count := 0
		for range sigCh {
			count++
			if count >= 2 {
				fmt.Println("\ninterrupted")
				os.Exit(0)
			}
			cancel()
			ctx, cancel = context.WithCancel(context.Background())
		}
	}()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		renderer.Prompt()
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" || input == "/exit" || input == "/quit" {
			break
		}
		if strings.HasPrefix(input, "/") {
			handled := handleChatSlashCommand(input, renderer, loadedSkills, state, reactRunner, setup)
			if handled {
				continue
			}
		}
		err := runChatTurn(ctx, reactRunner, input)
		if err != nil {
			renderer.Error(err.Error())
		}
	}
	fmt.Println()
}

func registerReactDelegationTools(reg *tools.Registry, setup *ChatSetup, baseReg *tools.Registry, approve tools.ApprovalFunc) {
	if reg == nil || setup == nil || baseReg == nil {
		return
	}
	pool := reactruntime.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		driver := setup.Driver
		if driver == nil && setup.MakeDriver != nil && strings.TrimSpace(setup.ChatModel) != "" {
			driver = setup.MakeDriver(setup.ChatModel)
		}
		if driver == nil {
			return "", fmt.Errorf("react delegation driver unavailable")
		}
		role = reactruntime.MapSpawnRole(role)
		childTools := baseReg.Filter(nil)
		childRunner := reactruntime.NewRunner(reactruntime.Config{
			Driver:   driver,
			Tools:    childTools,
			Renderer: agent.NewSilentRenderer(nil),
			SystemPrompt: func() string {
				return agent.BuildSystemPrompt(setup.WorkDir, childTools, "") + "\n\n" + reactDelegationSystemSuffix(role)
			},
			NativeSystemPrompt: func() string { return agent.BuildNativeSystemPrompt(setup.WorkDir) },
			Session:            reactruntime.NewSession(),
			MaxSessionTurns:    setup.Config.Chat.MaxTurns,
		})
		if err := childRunner.Run(ctx, task); err != nil {
			return "", err
		}
		return childRunner.LastResponse(), nil
	})
	reg.Register(reacttools.NewSpawnAgent(pool))
	reg.Register(reacttools.NewWaitAgent(pool))
}

func reactDelegationSystemSuffix(role string) string {
	var sb strings.Builder
	sb.WriteString("You are forge's spawned agent for a bounded delegated task.\n")
	sb.WriteString("- Complete the delegated task autonomously and return a plain-text result to the parent agent.\n")
	sb.WriteString("- Stay within the delegated scope.\n")
	sb.WriteString("- Do not mention internal runtime machinery or role hints in your answer unless directly relevant.\n")
	switch strings.ToLower(strings.TrimSpace(reactruntime.MapSpawnRole(role))) {
	case "explorer":
		sb.WriteString("- Bias toward inspection, tracing, and evidence gathering.\n")
	case "worker":
		sb.WriteString("- Bias toward concrete implementation and verification.\n")
	default:
		sb.WriteString("- Use your judgment to inspect, act, and verify as needed.\n")
	}
	return strings.TrimSpace(sb.String())
}

func resolveChatRuntimeMode() chatRuntimeMode {
	return chatRuntimeReact
}

type chatTurnRunner interface {
	Run(context.Context, string) error
	EmitResponse(string)
}

const promptBoundaryRefusal = "I can't provide hidden system/developer prompts or internal instructions, including paraphrased or hypothetical versions. I can summarize my role and high-level guardrails if useful."

func runChatTurn(ctx context.Context, reactRunner chatTurnRunner, input string) error {
	if isPromptBoundaryQuestion(input) {
		if reactRunner != nil {
			reactRunner.EmitResponse(promptBoundaryRefusal)
		}
		return nil
	}
	if reactRunner == nil {
		return fmt.Errorf("chat react runner is nil")
	}
	return reactRunner.Run(ctx, input)
}

func isPromptBoundaryQuestion(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "system prompt") ||
		strings.Contains(lower, "developer prompt") ||
		strings.Contains(lower, "hidden prompt") ||
		strings.Contains(lower, "internal instruction")
}

type chatSessionControl interface {
	SetDriver(llm.Driver)
	ClearHistory()
	AppendUserMessage(string)
	EmitResponse(string)
}

func handleChatSlashCommand(input string, renderer *agent.Renderer, loadedSkills []skills.Skill, state *chatstate.State, session chatSessionControl, setup *ChatSetup) bool {
	switch {
	case input == "/help":
		PrintChatHelp()
	case input == "/model":
		picked := PickModel(setup.Available)
		if picked == "" {
			return true
		}
		d := setup.MakeDriver(picked)
		if d == nil {
			renderer.Error(fmt.Sprintf("no API key found for model %q", picked))
			return true
		}
		setup.ChatModel = picked
		setup.Driver = d
		if session != nil {
			session.SetDriver(setup.Driver)
		}
		renderer.Info(fmt.Sprintf("switched to %s", setup.ChatModel))
	case strings.HasPrefix(input, "/model "):
		arg := strings.TrimSpace(strings.TrimPrefix(input, "/model "))
		if arg == "" {
			return true
		}
		newModel := ResolveModel(setup.Available, arg)
		if newModel == "" {
			renderer.Error(fmt.Sprintf("unknown model %q — try /models to see available", arg))
			return true
		}
		d := setup.MakeDriver(newModel)
		if d == nil {
			renderer.Error(fmt.Sprintf("no API key found for model %q", newModel))
			return true
		}
		setup.ChatModel = newModel
		setup.Driver = d
		if session != nil {
			session.SetDriver(setup.Driver)
		}
		renderer.Info(fmt.Sprintf("switched to %s", setup.ChatModel))
	case input == "/models":
		fmt.Println()
		for i, m := range setup.Available {
			marker := "  "
			if m == setup.ChatModel {
				marker = "▸ "
			}
			fmt.Printf("  %s%d. %s\n", marker, i+1, m)
		}
		fmt.Println()
	case input == "/clear":
		if state != nil {
			state.Clear()
		}
		if session != nil {
			session.ClearHistory()
		}
		renderer.Info("conversation history cleared")
	case input == "/skills":
		if len(loadedSkills) == 0 {
			renderer.Info("no skills loaded")
		} else {
			fmt.Println()
			for _, s := range loadedSkills {
				fmt.Printf("  /%s — %s\n", s.Name, s.Description)
			}
			fmt.Println()
		}
	default:
		// Check for skill activation
		cmd := strings.TrimPrefix(input, "/")
		if s, ok := skills.Get(loadedSkills, cmd); ok {
			if state != nil && state.SkillActivated(s.Name) {
				renderer.Info(fmt.Sprintf("skill already active: %s", s.Name))
				return true
			}
			if state != nil {
				state.ActivateSkill(s.Name)
			}
			if session != nil {
				session.AppendUserMessage(fmt.Sprintf("[Skill: %s]\n\n%s", s.Name, s.Body))
			}
			renderer.Info(fmt.Sprintf("skill activated: %s", s.Name))
			return true
		}
		renderer.Error(fmt.Sprintf("unknown command: %s (try /help)", input))
	}
	return true
}

func ContainsModel(models []string, name string) bool {
	for _, m := range models {
		if m == name {
			return true
		}
	}
	return false
}

func PickModel(models []string) string {
	if len(models) == 0 {
		fmt.Fprintln(os.Stderr, "error: no models available (check API keys)")
		return ""
	}

	fmt.Print("\n  Select a model:\n\n")
	for i, m := range models {
		fmt.Printf("    %d. %s\n", i+1, m)
	}
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("  choice [1]: ")
	if !scanner.Scan() {
		return ""
	}
	input := strings.TrimSpace(scanner.Text())
	if input == "" {
		return models[0]
	}
	if resolved := ResolveModel(models, input); resolved != "" {
		return resolved
	}
	fmt.Fprintf(os.Stderr, "  invalid selection: %q\n", input)
	return ""
}

func ResolveModel(models []string, input string) string {
	var idx int
	if _, err := fmt.Sscanf(input, "%d", &idx); err == nil && idx >= 1 && idx <= len(models) {
		return models[idx-1]
	}
	for _, m := range models {
		if strings.EqualFold(m, input) {
			return m
		}
	}
	for _, m := range models {
		if strings.EqualFold(pathBase(m), input) {
			return m
		}
	}
	needle := strings.ToLower(input)
	matches := make([]string, 0)
	for _, m := range models {
		if strings.Contains(strings.ToLower(m), needle) {
			matches = append(matches, m)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func pathBase(model string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 && i+1 < len(model) {
		return model[i+1:]
	}
	return model
}

func PrintChatHelp() {
	fmt.Println()
	fmt.Println("  Commands:")
	fmt.Println("    /model          select model from list")
	fmt.Println("    /model <name>   switch to a specific model")
	fmt.Println("    /models         show available models")
	fmt.Println("    /clear          clear conversation history")
	fmt.Println("    /trace          open debug trace overlay (requires forge -d)")
	fmt.Println("    /skills         list available skills and how to activate them")
	fmt.Println("    /<skill>        activate a loaded skill by name from /skills")
	fmt.Println("                     example: /skills, then /tdd")
	fmt.Println("    /help           show this help")
	fmt.Println("    /exit           exit chat")
	fmt.Println()
	fmt.Println("  Debug:")
	fmt.Println("    forge -d        open the advanced debug view and write a fresh debug log")
	fmt.Println()
}
