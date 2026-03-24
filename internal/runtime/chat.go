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
	"forge/internal/skills"
	"forge/internal/tui"
)

var (
	loadChatConfig    = bootstrap.LoadConfig
	loadChatTokens    = bootstrap.LoadTokens
	saveLastChatModel = config.SaveChatLastModel
	defaultConfigPath = config.DefaultPath
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
	} else if chatModel == "" || !ContainsModel(available, chatModel) {
		chatModel = PickModel(available)
		if chatModel == "" {
			return nil, nil
		}
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

	driver := makeChatDriver(chatModel)
	if driver == nil {
		return nil, fmt.Errorf("no API key found for model %q", chatModel)
	}
	persistChatLastModel(cfg, chatModel)

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

func registerTools(reg *tools.Registry, workDir string, cfg *config.Config, approve tools.ApprovalFunc, forcePrompt ...tools.ApprovalFunc) {
	fp := approve
	if len(forcePrompt) > 0 {
		fp = forcePrompt[0]
	}
	reg.Register(tools.NewReadFile(workDir))
	reg.Register(tools.NewWriteFile(workDir, approve))
	reg.Register(tools.NewEditFile(workDir, approve))
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
}

func RunChatLive(setup *ChatSetup) {
	eventsCh := make(chan llm.Event, 256)
	evRenderer := agent.NewEventRenderer(eventsCh)

	var approve tools.ApprovalFunc
	if setup.Yolo {
		approve = agent.YoloApproval()
	} else {
		approve = evRenderer.LiveApproval()
	}

	reg := tools.NewRegistry()
	registerTools(reg, setup.WorkDir, setup.Config, approve)
	baseReg := reg.Filter(nil)
	loadedSkills := skills.Load(setup.WorkDir)
	state := chatstate.New()

	a := agent.NewAgent(setup.Driver, reg, approve, setup.WorkDir, setup.Config.Chat.MaxTurns, evRenderer, loadedSkills, state)

	if setup.Config.Chat.Agents.Enabled {
		configureMultiAgent(a, baseReg, setup)
		evRenderer.SetLabel("dispatch")
	}

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
			go func(runMsg string) {
				err := a.Run(ctx, runMsg)
				inputCh <- runOutcome(err)
			}(msg)
		}
		for input := range inputCh {
			switch input {
			case "__approve_yes":
				evRenderer.ResponseChan() <- true
			case "__approve_no":
				evRenderer.ResponseChan() <- false
			case "__cancel_subagent__":
				a.CancelSubAgent()
				evRenderer.Info("sub-agent cancelled")
			case "__cancel_turn__":
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
					queue = append(queue, input)
					evRenderer.Info(fmt.Sprintf("queued steering: %s", input))
					continue
				}
				startRun(input)
			}
		}
	}()

	liveCfg := tui.ChatLiveConfig{
		Model:           setup.ChatModel,
		WorkDir:         setup.WorkDir,
		AvailableModels: setup.Available,
		Providers:       append([]tui.ProviderOption(nil), setup.Providers...),
		AgentsEnabled:   setup.Config.Chat.Agents.Enabled,
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
			a.SetDriver(setup.Driver)
			persistChatLastModel(setup.Config, name)
			return name, nil
		},
		ToggleAgents: func(enabled bool) error {
			setup.Config.Chat.Agents.Enabled = enabled
			if enabled {
				configureMultiAgent(a, baseReg, setup)
				evRenderer.SetLabel("dispatch")
				return nil
			}
			a.SetTools(baseReg.Filter(nil))
			a.UseGeneratedSystem()
			a.SetRole("")
			evRenderer.SetLabel("agent")
			return nil
		},
		GetAgentModels: func() map[string]string {
			return setup.Config.AgentRoleModels()
		},
		SaveAgentModels: func(models map[string]string) error {
			agentModels := config.AgentModels{
				Dispatch:  models["dispatch"],
				Scout:     models["scout"],
				Builder:   models["builder"],
				Doctor:    models["doctor"],
				Architect: models["architect"],
			}
			if err := config.SaveAgentModels(config.DefaultPath(), agentModels); err != nil {
				return err
			}
			setup.Config.Chat.Agents.Models = agentModels
			return nil
		},
		ClearHistory: func() {
			a.ClearHistory()
		},
		ApprovalCh:      evRenderer.ApprovalChan(),
		ResponseCh:      evRenderer.ResponseChan(),
		Skills:          loadedSkills,
		State:           state,
		CopilotClientID: setup.Config.CopilotClientID(),
	}
	tui.RunChatLive(eventsCh, liveCfg, inputCh, doneCh)
}

func configureMultiAgent(a *agent.Agent, baseReg *tools.Registry, setup *ChatSetup) {
	reg := baseReg.Filter(nil)
	mac := agent.MultiAgentConfig{
		Enabled:    true,
		RoleModels: setup.Config.AgentRoleModels(),
		MakeDriver: setup.MakeDriver,
		BaseTools:  reg,
	}

	// Register scratchpad tools on the base registry.
	reg.Register(tools.NewScratchpadWrite(setup.WorkDir))
	reg.Register(tools.NewScratchpadRead(setup.WorkDir))

	// Register the delegate tool (calls back into the agent).
	reg.Register(tools.NewDelegate(func(ctx context.Context, role, task string) (string, error) {
		return a.SpawnSubAgent(ctx, role, task, mac)
	}))

	// Switch the primary agent to dispatch role.
	dispatchRole := agent.Roles["dispatch"]
	a.SetSystem(agent.BuildSystemPrompt(setup.WorkDir, reg.Filter(dispatchRole.AllowTools), "") + "\n\n" + dispatchRole.System)
	a.SetTools(reg.Filter(dispatchRole.AllowTools))
	a.SetRole("dispatch")
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

	reg := tools.NewRegistry()
	interactiveApprove := agent.InteractiveApproval(os.Stdin, os.Stdout)
	registerTools(reg, setup.WorkDir, setup.Config, approve, interactiveApprove)
	loadedSkills := skills.Load(setup.WorkDir)

	renderer := agent.NewRenderer(os.Stdout, 80, true)
	state := chatstate.New()
	a := agent.NewAgent(setup.Driver, reg, approve, setup.WorkDir, setup.Config.Chat.MaxTurns, renderer, loadedSkills, state)

	if setup.Config.Chat.Agents.Enabled {
		configureMultiAgent(a, reg, setup)
	}

	fmt.Printf("forge chat (%s) — %s\n", setup.ChatModel, setup.WorkDir)
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
			handled := handleChatSlashCommand(input, renderer, a, setup)
			if handled {
				continue
			}
		}
		if err := a.Run(ctx, input); err != nil {
			renderer.Error(err.Error())
		}
	}
	fmt.Println()
}

func handleChatSlashCommand(input string, renderer *agent.Renderer, a *agent.Agent, setup *ChatSetup) bool {
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
		a.SetDriver(setup.Driver)
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
		a.SetDriver(setup.Driver)
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
	case input == "/expand":
		if exp := renderer.LastExpandable(); exp != "" {
			fmt.Fprintln(os.Stdout, exp)
			renderer.ClearExpandable()
		} else {
			renderer.Error("nothing to expand")
		}
	case input == "/clear":
		a.ClearHistory()
		renderer.Info("conversation history cleared")
	case input == "/skills":
		loaded := a.Skills()
		if len(loaded) == 0 {
			renderer.Info("no skills loaded")
		} else {
			fmt.Println()
			for _, s := range loaded {
				fmt.Printf("  /%s — %s\n", s.Name, s.Description)
			}
			fmt.Println()
		}
	default:
		// Check for skill activation
		cmd := strings.TrimPrefix(input, "/")
		if s, ok := skills.Get(a.Skills(), cmd); ok {
			a.InjectSkill(s)
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
	fmt.Println("    /expand         show full output of last truncated result")
	fmt.Println("    /clear          clear conversation history")
	fmt.Println("    /skills         list available skills and how to activate them")
	fmt.Println("    /<skill>        activate a loaded skill by name from /skills")
	fmt.Println("                     example: /skills, then /tdd")
	fmt.Println("    /help           show this help")
	fmt.Println("    /exit           exit chat")
	fmt.Println()
}
