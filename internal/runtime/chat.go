package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"forge/internal/agent"
	"forge/internal/agent/tools"
	"forge/internal/auth"
	"forge/internal/bootstrap"
	"forge/internal/chatstate"
	"forge/internal/codexusage"
	"forge/internal/config"
	"forge/internal/copilot"
	"forge/internal/hooks"
	"forge/internal/llm"
	"forge/internal/mcp"
	"forge/internal/memory"
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
	newChatMCPManager = func() *mcp.Manager { return mcp.NewManager() }
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
		return llm.NewRetryDriverWithIdleTimeout(d,
			effectiveCfg.Retry.MaxAttempts,
			time.Duration(effectiveCfg.Retry.InitialWait)*time.Millisecond,
			time.Duration(effectiveCfg.Retry.MaxWait)*time.Millisecond,
			time.Duration(effectiveCfg.Retry.Timeout)*time.Second,
			time.Duration(effectiveCfg.Resilience.StreamIdleTimeoutMS)*time.Millisecond,
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

func registerTools(reg *tools.Registry, workDir string, cfg *config.Config, session *reactruntime.Session, approve tools.ApprovalFunc, notify func(string), emitCommandStatus func(tools.ExecSessionStatus), forcePrompt ...tools.ApprovalFunc) (*tools.PreviewRuntime, *mcp.Manager) {
	fp := approve
	if len(forcePrompt) > 0 {
		fp = forcePrompt[0]
	}
	previewRuntime := tools.NewPreviewRuntime(workDir, approve)
	execManager := tools.NewExecSessionManager()
	execManager.SetEventHandler(func(status tools.ExecSessionStatus) {
		if emitCommandStatus != nil {
			emitCommandStatus(status)
			return
		}
		if notify != nil {
			notify(fmt.Sprintf("command session %d changed state", status.SessionID))
		}
	})
	mcpManager := newChatMCPManager()
	if notify != nil {
		mcpManager.SetEventHandler(func(ev mcp.Event) {
			switch ev.Kind {
			case mcp.EventToolsChanged, mcp.EventResourcesChanged:
				for _, def := range ev.Snapshot.Tools {
					reg.Register(tools.NewMCPDynamicTool(def, mcpManager))
				}
				notify(fmt.Sprintf("%s (%s)", strings.TrimSpace(ev.Message), ev.ServerName))
			case mcp.EventResourceUpdated:
				notify(fmt.Sprintf("MCP resource updated on %s: %s", ev.ServerName, ev.URI))
			case mcp.EventLogMessage, mcp.EventProgress:
				notify(fmt.Sprintf("%s (%s)", strings.TrimSpace(ev.Message), ev.ServerName))
			case mcp.EventRefreshed:
			default:
			}
		})
	}
	_ = mcpManager.Refresh(context.Background(), cfg)
	reg.Register(tools.NewReadFile(workDir))
	reg.Register(tools.NewWriteFile(workDir, approve))
	reg.Register(tools.NewEditFile(workDir, approve))
	reg.Register(tools.NewApplyPatch(workDir, approve))
	reg.Register(tools.NewArtifactWrite(previewRuntime))
	reg.Register(tools.NewArtifactRead(previewRuntime))
	reg.Register(tools.NewPreviewServerEnsure(previewRuntime))
	reg.Register(tools.NewPreviewServerStatus(previewRuntime))
	reg.Register(tools.NewListDir(workDir, cfg.Chat.IgnoreDirs))
	reg.Register(tools.NewSearch(workDir))
	reg.Register(tools.NewCodeSearch(workDir))
	reg.Register(tools.NewLSPDefinition(workDir))
	reg.Register(tools.NewLSPReferences(workDir))
	reg.Register(tools.NewLSPHover(workDir))
	reg.Register(tools.NewLSPDocumentSymbols(workDir))
	reg.Register(tools.NewRunCommand(workDir, cfg.Chat.CommandTimeout, execManager, approve, fp))
	reg.Register(tools.NewExecSessionStart(workDir, execManager, approve))
	reg.Register(tools.NewExecSessionStatus(execManager))
	reg.Register(tools.NewExecSessionWrite(execManager))
	reg.Register(tools.NewExecSessionResize(execManager))
	reg.Register(tools.NewExecSessionStop(execManager))
	reg.Register(tools.NewCommandStatus(execManager))
	reg.Register(tools.NewCommandWriteStdin(execManager))
	reg.Register(tools.NewThink())
	reg.Register(tools.NewGlob(workDir, cfg.Chat.IgnoreDirs))
	reg.Register(tools.NewGitStatus(workDir))
	reg.Register(tools.NewGitDiff(workDir))
	reg.Register(tools.NewGitLog(workDir))
	reg.Register(tools.NewGitBranchState(workDir))
	reg.Register(tools.NewGitMergeStatus(workDir))
	reg.Register(tools.NewViewImage(workDir))
	reg.Register(tools.NewToolHelp(reg))
	reg.Register(reacttools.NewUpdatePlan(session))
	reg.Register(reacttools.NewEnterPlanMode(session))
	reg.Register(reacttools.NewExitPlanMode(session))
	reg.Register(reacttools.NewAskUserQuestion())
	if mcpManager.HasServers() {
		reg.Register(tools.NewListMCPResources(mcpManager))
		reg.Register(tools.NewListMCPResourceTemplates(mcpManager))
		reg.Register(tools.NewReadMCPResource(mcpManager))
		for _, def := range mcpManager.Tools() {
			reg.Register(tools.NewMCPDynamicTool(def, mcpManager))
		}
	}
	gitCommit := tools.NewGitCommit(workDir, approve)
	gitCommit.PromptVisibility = tools.PromptHidden
	reg.Register(gitCommit)
	webFetch := tools.NewWebFetch()
	webFetch.PromptVisibility = tools.PromptHidden
	reg.Register(webFetch)
	webSearch := tools.NewWebSearch()
	webSearch.PromptVisibility = tools.PromptHidden
	reg.Register(webSearch)
	return previewRuntime, mcpManager
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
	session := reactruntime.NewSession()

	var approve tools.ApprovalFunc
	gate := reactruntime.NewApprovalGate(setup.WorkDir, reactruntime.LoadApprovalConfig(setup.Config), nil, func(text string) {
		evRenderer.Info(text)
	})
	gate.SetGuardianReviewer(func(transcript string, action tools.Action) tools.GuardianReview {
		return tools.ReviewApprovalAction(transcript, action)
	})
	gate.SetGuardianContext(func() string {
		return reactruntime.CompactGuardianContext(session.Snapshot())
	})
	gate.SetGuardianObserver(func(event reactruntime.GuardianEvent) {
		applyGuardianOverlay(session, event)
	})
	if setup.Yolo {
		approve = agent.YoloApproval()
	} else {
		approve = evRenderer.LiveApproval()
	}
	gate.SetPrompt(approve)
	approve = gate.Approve
	defer gate.Restore()

	reg := tools.NewRegistry()
	previewRuntime, mcpManager := registerTools(reg, setup.WorkDir, setup.Config, session, approve, evRenderer.Info, func(status tools.ExecSessionStatus) {
		payload, err := json.Marshal(status)
		if err != nil {
			evRenderer.Info(fmt.Sprintf("command session %d changed state", status.SessionID))
			return
		}
		evRenderer.ToolResult("command_status", string(payload), "", false)
	})
	if previewRuntime != nil {
		defer previewRuntime.Close()
	}
	if mcpManager != nil {
		defer func() { _ = mcpManager.Close() }()
	}
	baseReg := reg.Filter(nil)
	loadedSkills := skills.Load(setup.WorkDir)
	state := chatstate.New()
	memPipeline := memory.Pipeline{MaxRecords: 12}
	memState := memory.State{}
	reactRunner := reactruntime.NewRunner(reactruntime.Config{
		Driver:   setup.Driver,
		Tools:    reg,
		Renderer: evRenderer,
		SystemPrompt: func() string {
			snap := session.Snapshot()
			return agent.BuildNativeSystemPromptForMode(setup.WorkDir, string(snap.Mode), snap.TaskState != nil)
		},
		Session: session,
		TurnComplete: func(snapshot reactruntime.SessionSnapshot) {
			if next, ok := memPipeline.Process(memState, snapshot); ok {
				memState = next
				session.SetMemorySummary(next.Summary)
			}
		},
		Progress: func(text string) {
			evRenderer.Progress(text)
		},
		CompactionMaxFailures: setup.Config.Resilience.CompactionMaxFailures,
		Interactive:           true,
	})
	registerReactDelegationTools(reg, setup, baseReg, approve)

	inputCh := make(chan string, 1)
	doneCh := make(chan struct{}, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var turnCancel atomic.Value

	// nudgeForwarder routes nudge calls from the goroutine to liveCfg.NotifyNudge.
	// The bubbletea layer publishes its wrapped NotifyNudge into nudgeSink after
	// creating the tea program, so goroutine calls also trigger p.Send.
	var nudgeSink func(string, string, string)
	nudgeForwarder := func(mode, taskOp, suggestedSkill string) {
		if nudgeSink != nil {
			nudgeSink(mode, taskOp, suggestedSkill)
		}
	}
	var wg sync.WaitGroup

	go func() {
		defer wg.Wait()
		var running bool
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
			applySuggestedSkillOverlay(session, msg, loadedSkills, state)
			if nudge, skillName := suggestedSkillNudgeWithName(msg, loadedSkills, state); nudge != "" {
				evRenderer.Info(nudge)
				if nudgeForwarder != nil {
					nudgeForwarder("", "", skillName)
				}
			}
			turnCtx, tc := context.WithCancel(ctx)
			turnCancel.Store(tc)
			wg.Add(1)
			go func(runMsg string) {
				defer wg.Done()
				err := runChatTurn(turnCtx, reactRunner, runMsg)
				if turnCtx.Err() != nil {
					inputCh <- "__turn_failed__"
					return
				}
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
					reactRunner.MarkInterrupted()
					if tc, ok := turnCancel.Load().(context.CancelFunc); ok {
						tc()
					}
					_ = reactRunner.DiscardPendingInput()
					running = false
					evRenderer.Info("turn canceled")
				}
			case "__turn_done__":
				evRenderer.TurnDone()
				running = false
			case "__turn_failed__":
				evRenderer.TurnDone()
				running = false
			default:
				if running {
					if setup != nil && setup.debugRec != nil {
						setup.debugRec.logInput("queued", input)
					}
					reactRunner.QueuePendingInput(input)
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
		AutoSkillsMode:  setup.Config.Chat.AutoSkills,
		State:           state,
		CopilotClientID: setup.Config.CopilotClientID(),
	}
	// NotifyNudge is intentionally set after the struct literal so that
	// nudgeForwarder can be assigned the same function reference. The goroutine
	// captures nudgeForwarder by closure, so it calls the correct function once
	// populated. The bubbletea layer wraps liveCfg.NotifyNudge with p.Send, so
	// calls through nudgeForwarder also reach the TUI mode badge.
	liveCfg.NotifyNudge = func(mode, taskOp, suggestedSkill string) { /* forwarded by bubbletea */ }
	liveCfg.NotifyNudgeSink = &nudgeSink
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
	session := reactruntime.NewSession()
	var approve tools.ApprovalFunc
	if setup.Yolo {
		approve = agent.YoloApproval()
	} else {
		approve = agent.InteractiveApproval(os.Stdin, os.Stdout)
	}
	gate := reactruntime.NewApprovalGate(setup.WorkDir, reactruntime.LoadApprovalConfig(setup.Config), approve, func(text string) {
		_, _ = fmt.Fprintln(os.Stdout, text)
	})
	gate.SetGuardianReviewer(func(transcript string, action tools.Action) tools.GuardianReview {
		return tools.ReviewApprovalAction(transcript, action)
	})
	gate.SetGuardianContext(func() string {
		return reactruntime.CompactGuardianContext(session.Snapshot())
	})
	gate.SetGuardianObserver(func(event reactruntime.GuardianEvent) {
		applyGuardianOverlay(session, event)
	})
	approve = gate.Approve
	defer gate.Restore()

	reg := tools.NewRegistry()
	renderer := agent.NewRenderer(os.Stdout, 80, true)
	forcePromptApprove := approve
	previewRuntime, mcpManager := registerTools(reg, setup.WorkDir, setup.Config, session, approve, renderer.Info, func(status tools.ExecSessionStatus) {
		payload, err := json.Marshal(status)
		if err != nil {
			renderer.Info(fmt.Sprintf("command session %d changed state", status.SessionID))
			return
		}
		renderer.ToolResult("command_status", string(payload), "", false)
	}, forcePromptApprove)
	if previewRuntime != nil {
		defer previewRuntime.Close()
	}
	if mcpManager != nil {
		defer func() { _ = mcpManager.Close() }()
	}
	baseReg := reg.Filter(nil)
	loadedSkills := skills.Load(setup.WorkDir)
	state := chatstate.New()
	memPipeline := memory.Pipeline{MaxRecords: 12}
	memState := memory.State{}
	reactRunner := reactruntime.NewRunner(reactruntime.Config{
		Driver:   setup.Driver,
		Tools:    reg,
		Renderer: renderer,
		SystemPrompt: func() string {
			snap := session.Snapshot()
			base := agent.BuildNativeSystemPromptForMode(setup.WorkDir, string(snap.Mode), snap.TaskState != nil)
			if skillText := skills.Describe(loadedSkills); skillText != "" {
				base += "\n\n" + skillText
			}
			return base
		},
		Session: session,
		TurnComplete: func(snapshot reactruntime.SessionSnapshot) {
			if next, ok := memPipeline.Process(memState, snapshot); ok {
				memState = next
				session.SetMemorySummary(next.Summary)
			}
		},
		Progress: func(text string) {
			renderer.Progress(text)
		},
		CompactionMaxFailures: setup.Config.Resilience.CompactionMaxFailures,
		Interactive:           true,
	})
	registerReactDelegationTools(reg, setup, baseReg, approve)

	fmt.Printf("forge (%s) — %s\n", setup.ChatModel, setup.WorkDir)
	fmt.Println("type your request, or /help for commands")
	fmt.Println()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var turnCancel atomic.Value

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	pendingShutdown := make(chan struct{})
	go func() {
		count := 0
		for range sigCh {
			count++
			if count >= 2 {
				select {
				case <-pendingShutdown:
				default:
					close(pendingShutdown)
				}
				return
			}
			cancel()
			nctx, nc := context.WithCancel(context.Background())
			ctx, cancel = nctx, nc
			if tc, ok := turnCancel.Load().(context.CancelFunc); ok {
				tc()
			}
		}
	}()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		select {
		case <-pendingShutdown:
			fmt.Println("\ninterrupted")
			return
		default:
		}
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
		if setup.Config.Chat.AutoSkills == skills.AutoSkillsAuto {
			if s, ok := skills.DetectAuto(loadedSkills, input); ok {
				state.ActivateSkill(s.Name)
				renderer.Info(fmt.Sprintf("skill activated: %s", s.Name))
				input = skills.SkillMessageWithUserInput(s, input)
			}
		}
		applySuggestedSkillOverlay(session, input, loadedSkills, state)
		if nudge := suggestedSkillNudge(input, loadedSkills, state); nudge != "" {
			renderer.Info(nudge)
		}
		turnCtx, tc := context.WithCancel(ctx)
		turnCancel.Store(tc)
		err := runChatTurn(turnCtx, reactRunner, input)
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
				return agent.BuildNativeSystemPromptForMode(setup.WorkDir, "", false)
			},
			Session:               reactruntime.NewSession(),
			CompactionMaxFailures: setup.Config.Resilience.CompactionMaxFailures,
			Interactive:           false,
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
	SetTaskState(reactruntime.TaskState)
	TaskState() *reactruntime.TaskState
	QueuePendingInput(string)
	DiscardPendingInput() []string
	MarkInterrupted()
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
	if shouldPromoteFollowUpToImplementation(input, reactRunner.TaskState()) {
		current := reactRunner.TaskState()
		next := reactruntime.TaskState{
			Objective:            strings.TrimSpace(input),
			Operation:            "implement",
			RequiredVerification: "inspect the relevant code, make the change with edit tools, and run the relevant verification before claiming completion",
		}
		if current != nil && strings.TrimSpace(current.Objective) != "" {
			next.Objective = strings.TrimSpace(current.Objective)
		}
		reactRunner.SetTaskState(next)
	} else if shouldResetTaskStateForInput(input) {
		reactRunner.SetTaskState(reactruntime.TaskState{})
	}
	return reactRunner.Run(ctx, input)
}

func suggestedSkillNudge(input string, loadedSkills []skills.Skill, state *chatstate.State) string {
	nudge, _ := suggestedSkillNudgeWithName(input, loadedSkills, state)
	return nudge
}

// suggestedSkillNudgeWithName returns the human-readable nudge string and the
// raw skill name separately so callers can forward the name to NotifyNudge.
func suggestedSkillNudgeWithName(input string, loadedSkills []skills.Skill, state *chatstate.State) (nudge, skillName string) {
	if len(loadedSkills) == 0 || strings.TrimSpace(input) == "" {
		return "", ""
	}
	active := map[string]bool{}
	if state != nil {
		for _, name := range state.ActiveSkills() {
			active[name] = true
		}
	}
	suggestion, ok := skills.Suggest(loadedSkills, "", input, active)
	if !ok {
		return "", ""
	}
	return fmt.Sprintf("suggested skill: /%s (%s)", suggestion.Name, suggestion.Reason), suggestion.Name
}

func applySuggestedSkillOverlay(session *reactruntime.Session, input string, loadedSkills []skills.Skill, state *chatstate.State) {
	if session == nil {
		return
	}
	registry := newChatHookRegistry()
	session.SetHookOutput(chatPromptHookOutput(context.Background(), session, registry, chatPromptHookPayload{
		SuggestedSkillNudge: suggestedSkillNudge(input, loadedSkills, state),
	}, "suggested_skill"))
}

func applyGuardianOverlay(session *reactruntime.Session, event reactruntime.GuardianEvent) {
	if session == nil {
		return
	}
	registry := newChatHookRegistry()
	session.SetHookOutput(chatPromptHookOutput(context.Background(), session, registry, chatPromptHookPayload{
		GuardianEvent: &event,
	}, "guardian_warning"))
}

type chatPromptHookPayload struct {
	SuggestedSkillNudge string
	GuardianEvent       *reactruntime.GuardianEvent
}

func newChatHookRegistry() *hooks.Registry {
	registry := hooks.NewRegistry()
	registry.Register(hooks.PointPromptContext, "suggested_skill", suggestedSkillPromptHook)
	registry.Register(hooks.PointPromptContext, "guardian_warning", guardianWarningPromptHook)
	return registry
}

func suggestedSkillPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(chatPromptHookPayload)
	if !ok {
		return nil
	}
	nudge := strings.TrimSpace(payload.SuggestedSkillNudge)
	if nudge == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "suggested_skill",
		Content:    nudge,
		Priority:   hooks.PriorityNormal,
		Provenance: "runtime",
	}}
}

func guardianWarningPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(chatPromptHookPayload)
	if !ok || payload.GuardianEvent == nil {
		return nil
	}
	switch payload.GuardianEvent.Decision {
	case tools.GuardianWarn, tools.GuardianBlock:
		reason := strings.TrimSpace(payload.GuardianEvent.Reason)
		if reason == "" {
			reason = "approval action needs extra scrutiny"
		}
		content := "Guardian " + strings.ToLower(string(payload.GuardianEvent.Decision)) + ": " + reason
		if summary := strings.TrimSpace(payload.GuardianEvent.Action.Summary); summary != "" {
			content += "\nAction: " + summary
		}
		return []hooks.Result{hooks.OverlayResult{
			Key:        "guardian_warning",
			Content:    content,
			Priority:   hooks.PriorityHigh,
			Provenance: "runtime",
		}}
	default:
		return nil
	}
}

func chatPromptHookOutput(ctx context.Context, session *reactruntime.Session, registry *hooks.Registry, payload chatPromptHookPayload, ownedKeys ...string) hooks.ExecutionOutput {
	base, snapshot := currentChatPromptHookOutput(session)
	base.Overlays = filterChatHookOverlays(base.Overlays, ownedKeys...)
	if registry == nil {
		return base
	}
	runtime := registry.Dispatch(ctx, hooks.Event{
		Point:     hooks.PointPromptContext,
		Snapshot:  snapshot,
		Transient: payload,
	})
	return mergeChatHookOutput(base, runtime)
}

func currentChatPromptHookOutput(session *reactruntime.Session) (hooks.ExecutionOutput, reactruntime.SessionSnapshot) {
	if session == nil {
		return hooks.ExecutionOutput{}, reactruntime.SessionSnapshot{}
	}
	snapshot := session.Snapshot()
	if snapshot.HookOutputSet {
		return cloneChatHookOutput(snapshot.HookOutput), snapshot
	}
	output := hooks.ExecutionOutput{}
	if len(snapshot.HookOverlays) > 0 {
		output.Overlays = append([]hooks.OverlayResult(nil), snapshot.HookOverlays...)
	}
	if note := strings.TrimSpace(snapshot.RuntimeNote); note != "" {
		output.Note = &hooks.NoteResult{Message: note}
	}
	return output, snapshot
}

func filterChatHookOverlays(overlays []hooks.OverlayResult, ownedKeys ...string) []hooks.OverlayResult {
	if len(overlays) == 0 {
		return nil
	}
	if len(ownedKeys) == 0 {
		return append([]hooks.OverlayResult(nil), overlays...)
	}
	owned := make(map[string]struct{}, len(ownedKeys))
	for _, key := range ownedKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		owned[strings.ToLower(key)] = struct{}{}
	}
	filtered := make([]hooks.OverlayResult, 0, len(overlays))
	for _, overlay := range overlays {
		if _, ok := owned[strings.ToLower(strings.TrimSpace(overlay.Key))]; ok {
			continue
		}
		filtered = append(filtered, overlay)
	}
	return filtered
}

func mergeChatHookOutput(base, runtime hooks.ExecutionOutput) hooks.ExecutionOutput {
	merged := hooks.ExecutionOutput{
		Overlays: append([]hooks.OverlayResult(nil), base.Overlays...),
		Failures: append([]hooks.Failure(nil), runtime.Failures...),
	}
	if base.Note != nil {
		note := *base.Note
		merged.Note = &note
	}
	if runtime.Note != nil && (merged.Note == nil || runtime.Note.Priority > merged.Note.Priority) {
		note := *runtime.Note
		merged.Note = &note
	}
	merged.Overlays = append(merged.Overlays, runtime.Overlays...)
	return merged
}

func cloneChatHookOutput(output hooks.ExecutionOutput) hooks.ExecutionOutput {
	cloned := hooks.ExecutionOutput{
		Overlays: append([]hooks.OverlayResult(nil), output.Overlays...),
		Failures: append([]hooks.Failure(nil), output.Failures...),
	}
	if output.Note != nil {
		note := *output.Note
		cloned.Note = &note
	}
	if output.Block != nil {
		block := *output.Block
		cloned.Block = &block
	}
	return cloned
}

func normalizedIntentText(input string) string {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return ""
	}
	text = strings.NewReplacer("’", "'", "“", "\"", "”", "\"").Replace(text)
	text = collapseRepeatedLetters(text)
	return strings.Join(strings.Fields(text), " ")
}

func shouldResetTaskStateForInput(input string) bool {
	text := normalizedIntentText(input)
	if text == "" || looksLikeWorkspaceScopedInput(text) || looksLikeRepoFollowUp(text) {
		return false
	}
	return true
}

func shouldPromoteFollowUpToImplementation(input string, state *reactruntime.TaskState) bool {
	if state == nil {
		return false
	}
	operation := strings.ToLower(strings.TrimSpace(state.Operation))
	if operation != "inspect" && operation != "overview" && operation != "analysis" {
		return false
	}
	text := normalizedIntentText(input)
	return containsAnyPhrase(text,
		"do it", "use what you need", "go ahead", "implement it", "build it",
		"make the change", "make changes", "fix it", "apply it", "ship it",
	)
}

func looksLikeWorkspaceScopedInput(text string) bool {
	return containsAnyPhrase(text,
		"repo", "repository", "project", "codebase", "workspace", "worktree",
		"working directory", "current directory", "this directory", "this folder",
		"this repo", "this project", "this codebase", "branch", "diff",
		"pull request", "in here",
	)
}

func looksLikeRepoFollowUp(text string) bool {
	return containsAnyPhrase(text,
		"do it", "fix it", "apply it", "apply that", "commit it", "merge it",
		"test it", "build it", "validate it", "finish it", "continue with it",
		"go ahead", "ship it", "run the tests", "run tests", "rerun tests",
		"run the build", "run build", "keep going", "same problem", "same issue",
		"same again", "still broken", "still broke", "still the same",
		"what do you think", "tell me what you think", "anything i need change",
		"what should i change", "what should i improve", "clean this up",
		"write me a script", "script to clean",
	)
}

func collapseRepeatedLetters(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(text))
	var prev rune
	repeatCount := 0
	for _, r := range text {
		if r == prev && r >= 'a' && r <= 'z' {
			repeatCount++
			if repeatCount >= 2 {
				continue
			}
		} else {
			repeatCount = 0
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

func containsAnyPhrase(text string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func gitBranchContainsHead(workDir, branch string) (bool, error) {
	verify := exec.Command("git", "rev-parse", "--verify", "--quiet", branch)
	verify.Dir = workDir
	if err := verify.Run(); err != nil {
		return false, fmt.Errorf("verify target branch %s: %w", branch, err)
	}
	cmd := exec.Command("git", "merge-base", "--is-ancestor", "HEAD", branch)
	cmd.Dir = workDir
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("check whether %s contains HEAD: %w", branch, err)
	}
	return true, nil
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
	SetTaskState(reactruntime.TaskState)
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
