package runtime

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
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
	"forge/internal/fsutil"
	"forge/internal/hooks"
	"forge/internal/llm"
	"forge/internal/mcp"
	"forge/internal/memory"
	"forge/internal/modelcatalog"
	"forge/internal/permissions"
	pluginruntime "forge/internal/plugins"
	"forge/internal/protocol"
	reactruntime "forge/internal/react"
	reacttools "forge/internal/react/tools"
	forgesession "forge/internal/session"
	"forge/internal/sessionstore"
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

func loadChatApprovalConfig(setup *ChatSetup) reactruntime.ApprovalConfig {
	if setup == nil || setup.Config == nil {
		return reactruntime.LoadApprovalConfig(nil)
	}
	cfg := reactruntime.LoadApprovalConfig(setup.Config)
	if !setup.Config.Permissions.Auto.Enabled {
		return cfg
	}
	model := strings.TrimSpace(setup.Config.Permissions.Auto.Model)
	if model == "" {
		model = strings.TrimSpace(setup.Config.Models.Auditor)
	}
	if model == "" {
		model = strings.TrimSpace(setup.Config.Models.Summarizer)
	}
	if model == "" {
		model = strings.TrimSpace(setup.ChatModel)
	}
	if model == "" || setup.MakeDriver == nil {
		return cfg
	}
	driver := setup.MakeDriver(model)
	if driver == nil {
		return cfg
	}
	cfg.Classifier = newLLMPermissionClassifier(driver, time.Duration(setup.Config.Permissions.Auto.TimeoutMS)*time.Millisecond)
	cfg.Denials = permissions.NewDenialTracker(setup.Config.Permissions.Auto.MaxConsecutiveDenials, setup.Config.Permissions.Auto.MaxTotalDenials)
	cfg.ClassifierFailureBehavior = reactruntime.ClassifierFailureBehavior(setup.Config.Permissions.Auto.FailureBehavior)
	return cfg
}

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
	configureDurableSessionSink(cfg, session, workDir)
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
	secretPolicy := tools.SecretPolicy{
		Read:           tools.SecretPolicyMode(cfg.Security.Secrets.Read),
		Write:          tools.SecretPolicyMode(cfg.Security.Secrets.Write),
		CommandOutput:  tools.SecretPolicyMode(cfg.Security.Secrets.CommandOutput),
		ApprovalDetail: tools.SecretPolicyMode(cfg.Security.Secrets.ApprovalDetail),
	}
	workDirProvider := tools.FixedWorkDirProvider(workDir)
	if session != nil {
		workDirProvider = func() string {
			if root := session.Snapshot().ActiveWorkspaceRoot; root != "" {
				return root
			}
			return workDir
		}
	}
	if outputStore := configuredOutputStore(cfg); outputStore != nil {
		reg.Register(tools.NewReadOutput(outputStore, secretPolicy))
	}
	reg.Register(tools.NewReadFile(workDir, secretPolicy))
	reg.Register(tools.NewWriteFileWithWorkDirProvider(workDir, workDirProvider, approve, secretPolicy))
	reg.Register(tools.NewEditFileWithWorkDirProvider(workDir, workDirProvider, approve, secretPolicy))
	reg.Register(tools.NewApplyPatchWithWorkDirProvider(workDir, workDirProvider, approve, secretPolicy))
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
	reg.Register(tools.NewRunCommandWithWorkDirProvider(workDir, workDirProvider, cfg.Chat.CommandTimeout, execManager, approve, secretPolicy, fp))
	reg.Register(tools.NewExecSessionStartWithWorkDirProvider(workDir, workDirProvider, execManager, approve))
	reg.Register(tools.NewExecSessionStatus(execManager))
	reg.Register(tools.NewExecSessionWrite(execManager))
	reg.Register(tools.NewExecSessionResize(execManager))
	reg.Register(tools.NewExecSessionStop(execManager))
	reg.Register(tools.NewCommandStatus(execManager))
	reg.Register(tools.NewCommandWriteStdin(execManager))
	reg.Register(tools.NewThink())
	reg.Register(tools.NewGlob(workDir, cfg.Chat.IgnoreDirs))
	reg.Register(tools.NewGitStatusWithWorkDirProvider(workDir, workDirProvider))
	reg.Register(tools.NewGitDiffWithWorkDirProvider(workDir, workDirProvider))
	reg.Register(tools.NewGitLogWithWorkDirProvider(workDir, workDirProvider))
	reg.Register(tools.NewGitBranchStateWithWorkDirProvider(workDir, workDirProvider))
	reg.Register(tools.NewGitMergeStatusWithWorkDirProvider(workDir, workDirProvider))
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
	gitCommit := tools.NewGitCommitScopedWithWorkDirProvider(workDir, workDirProvider, approve, gitScopeProviderForSession(session))
	gitCommit.PromptVisibility = tools.PromptHidden
	reg.Register(gitCommit)
	gitPush := tools.NewGitPushScopedWithWorkDirProvider(workDir, workDirProvider, approve, gitScopeProviderForSession(session))
	gitPush.PromptVisibility = tools.PromptHidden
	reg.Register(gitPush)
	webFetch := tools.NewWebFetch()
	webFetch.PromptVisibility = tools.PromptHidden
	reg.Register(webFetch)
	webSearch := tools.NewWebSearch()
	webSearch.PromptVisibility = tools.PromptHidden
	reg.Register(webSearch)
	return previewRuntime, mcpManager
}

func gitScopeProviderForSession(session *reactruntime.Session) tools.GitScopeProvider {
	if session == nil {
		return nil
	}
	return func() tools.GitScope {
		intent := session.Snapshot().SideEffectIntent
		if intent == nil {
			return tools.GitScope{}
		}
		return tools.GitScope{
			AllowedPaths:  intent.AllowedPaths,
			TargetBranch:  intent.TargetBranch,
			Remote:        intent.Remote,
			RequireBranch: strings.TrimSpace(intent.TargetBranch) != "",
		}
	}
}

func configureDurableSessionSink(cfg *config.Config, session *reactruntime.Session, workDir string) {
	if cfg == nil || session == nil || strings.TrimSpace(cfg.Session.OutputDir) == "" {
		return
	}
	store := sessionstore.NewJSONLThreadStore(filepath.Join(cfg.Session.OutputDir, "threads"))
	model := strings.TrimSpace(cfg.Chat.Model)
	if model == "" {
		model = strings.TrimSpace(cfg.Chat.LastModel)
	}
	threadID := durableThreadID(session)
	session.SetDurableThreadID(threadID)
	live := sessionstore.NewLiveSession(threadID, store, sessionstore.DefaultPersistencePolicy())
	metadataErr := live.UpdateMetadata(context.Background(), sessionstore.ThreadMetadataPatch{
		Title:     "Forge chat",
		Preview:   strings.TrimSpace(session.Snapshot().InitialInput),
		CWD:       strings.TrimSpace(workDir),
		Model:     model,
		UpdatedAt: time.Now().UTC(),
	})
	session.SetDurableSink(live)
	session.AppendItem(protocol.Item{
		Kind:        protocol.ItemSessionMeta,
		SessionMeta: &protocol.SessionMetaItem{Source: "runtime", CWD: strings.TrimSpace(workDir), Model: model},
	})
	if metadataErr != nil {
		session.RecordDurableError(metadataErr)
	}
}

func configuredOutputStore(cfg *config.Config) sessionstore.OutputStore {
	if cfg == nil || strings.TrimSpace(cfg.Session.OutputDir) == "" {
		return nil
	}
	return sessionstore.NewFileOutputStore(cfg.Session.OutputDir)
}

func durableThreadID(session *reactruntime.Session) string {
	if session != nil {
		if snap := session.Snapshot(); strings.TrimSpace(snap.InitialInput) != "" {
			sum := sha256.Sum256([]byte(snap.InitialInput))
			return fmt.Sprintf("thread-%x", sum)[:24]
		}
	}
	return fmt.Sprintf("thread-%d", time.Now().UTC().UnixNano())
}

func startChatPluginManager(cfg *config.Config, workDir string, notify func(string)) *pluginruntime.Manager {
	if cfg == nil || len(cfg.Plugins) == 0 {
		return nil
	}
	manager := pluginruntime.NewManager(workDir, cfg.Plugins)
	if err := manager.Start(context.Background()); err != nil && notify != nil {
		notify("plugin service: " + err.Error())
	}
	if !manager.HasPlugins() {
		_ = manager.Close()
		return nil
	}
	return manager
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
	session.SetActiveWorkspaceRoot(setup.WorkDir)

	var approve tools.ApprovalFunc
	gate := reactruntime.NewApprovalGate(setup.WorkDir, loadChatApprovalConfig(setup), nil, func(text string) {
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
	pluginManager := startChatPluginManager(setup.Config, setup.WorkDir, evRenderer.Info)
	if pluginManager != nil {
		defer func() { _ = pluginManager.Close() }()
		pluginManager.RegisterTools(reg, approve)
	}
	baseReg := reg.Filter(nil)
	loadedSkills := skills.Load(setup.WorkDir)
	state := chatstate.New()
	memPipeline := memory.Pipeline{MaxRecords: 12}
	memState := memory.State{}
	lean := leanToolExposure(setup)
	reactRunner := reactruntime.NewRunner(reactruntime.Config{
		Driver:           setup.Driver,
		Tools:            reg,
		Renderer:         evRenderer,
		LeanToolExposure: lean,
		SystemPrompt: func() string {
			snap := session.Snapshot()
			base := agent.BuildNativeSystemPromptForMode(setup.WorkDir, string(snap.Mode), snap.TaskState != nil)
			if lean {
				if note := deferredToolsNote(reg); note != "" {
					base += "\n\n" + note
				}
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
		ToolExposureObserver: debugToolExposureObserver(setup),
		Progress: func(text string) {
			evRenderer.Progress(text)
		},
		ConfigureHooks: func(registry *hooks.Registry) {
			if pluginManager != nil {
				pluginManager.RegisterHooks(registry)
			}
		},
		CompactionMaxFailures:    setup.Config.Resilience.CompactionMaxFailures,
		Interactive:              true,
		ToolThrashCircuitBreaker: setup.Config.Resilience.ToolThrashCircuitBreaker,
		OutputStore:              configuredOutputStore(setup.Config),
	})
	registerReactDelegationTools(reg, setup, baseReg, approve, evRenderer, pluginManager, session)

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
		startRun := func(ui chatstate.ChatUserInput) {
			running = true
			text := ui.Text
			if setup != nil && setup.debugRec != nil {
				setup.debugRec.logInput("user", text)
			}
			applySuggestedSkillOverlay(session, text, loadedSkills, state)
			if nudge, skillName := suggestedSkillNudgeWithName(text, loadedSkills, state); nudge != "" {
				evRenderer.Info(nudge)
				if nudgeForwarder != nil {
					nudgeForwarder("", "", skillName)
				}
			}
			turnCtx, tc := context.WithCancel(ctx)
			turnCancel.Store(tc)
			wg.Add(1)
			go func(runInput chatstate.ChatUserInput) {
				defer wg.Done()
				err := runChatTurn(turnCtx, reactRunner, runInput)
				if turnCtx.Err() != nil {
					inputCh <- "__turn_failed__"
					return
				}
				inputCh <- runOutcome(err)
			}(ui)
		}
		for rawInput := range inputCh {
			// Try to decode as ChatUserInput
			var ui chatstate.ChatUserInput
			if err := json.Unmarshal([]byte(rawInput), &ui); err == nil && ui.IsInput {
				if len(ui.Attachments) > 0 && setup != nil && setup.debugRec != nil {
					setup.debugRec.logInput("user-attachments", fmt.Sprintf("%d image(s)", len(ui.Attachments)))
				}
				if running {
					if setup != nil && setup.debugRec != nil {
						setup.debugRec.logInput("queued", ui.Text)
					}
					if strings.TrimSpace(ui.SkillName) != "" || strings.TrimSpace(ui.SkillBody) != "" {
						reactRunner.AppendSkillContext(ui.SkillName, ui.SkillBody)
					}
					reactRunner.QueuePendingInput(ui.Text)
					evRenderer.Info(fmt.Sprintf("queued steering: %s", ui.Text))
					continue
				}
				startRun(ui)
				continue
			}

			// Legacy control/string handling
			switch rawInput {
			case "__approve_yes":
				if setup != nil && setup.debugRec != nil {
					setup.debugRec.logInput("control", rawInput)
				}
				evRenderer.ResponseChan() <- true
			case "__approve_no":
				if setup != nil && setup.debugRec != nil {
					setup.debugRec.logInput("control", rawInput)
				}
				evRenderer.ResponseChan() <- false
			case "__cancel_turn__":
				if setup != nil && setup.debugRec != nil {
					setup.debugRec.logInput("control", rawInput)
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
						setup.debugRec.logInput("queued", rawInput)
					}
					reactRunner.QueuePendingInput(rawInput)
					evRenderer.Info(fmt.Sprintf("queued steering: %s", rawInput))
					continue
				}
				startRun(chatstate.ChatUserInput{IsInput: true, Text: rawInput})
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
		CurrentThreadID: func() string {
			return session.DurableThreadID()
		},
		RestoreHistory: func(threadID string) (int, error) {
			outputDir := strings.TrimSpace(setup.Config.Session.OutputDir)
			if outputDir == "" {
				return 0, fmt.Errorf("session output dir not configured")
			}
			store := sessionstore.NewJSONLThreadStore(filepath.Join(outputDir, "threads"))
			items, err := store.ReadItems(context.Background(), threadID)
			if err != nil {
				return 0, err
			}
			if len(items) == 0 {
				return 0, fmt.Errorf("thread %s not found", threadID)
			}
			return session.AdoptReplayItems(items)
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
	// StartPipeline starts a pipeline session from within chat mode.
	// Pipeline events are forwarded through renderCh so the ChatModel can process them
	// and the debug logger can capture them.
	liveCfg.StartPipeline = func(prompt, writerModel, auditorModel string, rounds int) error {
		go func() {
			cfg, tokens := refreshChatSetupState(setup)
			// Build a minimal registry with the required models
			reg := llm.NewRegistry()
			bootstrap.EnsureDriver(cfg, tokens, reg, writerModel)
			bootstrap.EnsureDriver(cfg, tokens, reg, auditorModel)
			bootstrap.EnsureDriver(cfg, tokens, reg, cfg.Models.Summarizer)

			gate := forgesession.NewTurnGate()
			tracker := llm.NewUsageTracker()

			started := tui.SessionStarted{
				Prompt:       prompt,
				WriterModel:  writerModel,
				AuditorModel: auditorModel,
				Rounds:       rounds,
				WorkDir:      setup.WorkDir,
			}

			pipelineEvents, _ := StartSession(ctx, cfg, tokens, reg, started, gate, "", tracker, nil)
			for ev := range pipelineEvents {
				select {
				case renderCh <- ev:
				case <-ctx.Done():
					return
				}
			}
		}()
		return nil
	}
	// LoadPipelineDefaults reads saved pipeline model defaults from config.
	liveCfg.LoadPipelineDefaults = func() (string, string, int) {
		cfg, _ := refreshChatSetupState(setup)
		if cfg == nil {
			return "", "", 0
		}
		return cfg.Models.Writer, cfg.Models.Auditor, cfg.Session.RoundsPerPass
	}
	// SavePipelineDefaults persists pipeline model defaults to config.
	liveCfg.SavePipelineDefaults = func(writerModel, auditorModel string, rounds int) {
		cfg, _ := refreshChatSetupState(setup)
		if cfg == nil {
			return
		}
		cfg.Models.Writer = writerModel
		cfg.Models.Auditor = auditorModel
		cfg.Session.RoundsPerPass = rounds
		path := defaultConfigPath()
		_ = config.Save(path, cfg)
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

// leanToolExposure decides whether to expose the reduced tool-schema set.
// Explicit config wins; otherwise lean is auto-enabled for local/self-hosted
// providers, where huge tool menus burn context and confuse small models.
func leanToolExposure(setup *ChatSetup) bool {
	if setup == nil || setup.Config == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(setup.Config.Chat.ToolProfile)) {
	case "lean":
		return true
	case "full":
		return false
	}
	provider, _, ok := strings.Cut(strings.TrimSpace(setup.ChatModel), "/")
	if !ok {
		return false
	}
	defs, err := bootstrap.LoadCustomCompatProviders(fsutil.ForgeConfigDir())
	if err != nil {
		return false
	}
	for _, def := range defs {
		if def.ID == provider {
			return isLocalBaseURL(def.BaseURL)
		}
	}
	return false
}

func isLocalBaseURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	// ponytail: plain-http named hosts are treated as LAN; hosted APIs are https
	return u.Scheme == "http"
}

// deferredToolsNote lists registered tools whose schemas are not attached
// under lean exposure, so the model knows they exist and how to reach them.
func deferredToolsNote(reg *tools.Registry) string {
	core := make(map[string]bool, len(reactruntime.LeanCoreToolNames))
	for _, name := range reactruntime.LeanCoreToolNames {
		core[name] = true
	}
	var names []string
	for _, tool := range reg.All() {
		if name := strings.TrimSpace(tool.Name); name != "" && !core[name] {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return "## Additional Tools\nThese tools also exist but their schemas are not attached: " + strings.Join(names, ", ") + ".\nTo use one, first call tool_help with its name to get its parameters, then call it like any other tool."
}

func RunChatConsole(setup *ChatSetup) {
	session := reactruntime.NewSession()
	session.SetActiveWorkspaceRoot(setup.WorkDir)
	var approve tools.ApprovalFunc
	if setup.Yolo {
		approve = agent.YoloApproval()
	} else {
		approve = agent.InteractiveApproval(os.Stdin, os.Stdout)
	}
	gate := reactruntime.NewApprovalGate(setup.WorkDir, loadChatApprovalConfig(setup), approve, func(text string) {
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
	pluginManager := startChatPluginManager(setup.Config, setup.WorkDir, renderer.Info)
	if pluginManager != nil {
		defer func() { _ = pluginManager.Close() }()
		pluginManager.RegisterTools(reg, approve)
	}
	baseReg := reg.Filter(nil)
	loadedSkills := skills.Load(setup.WorkDir)
	state := chatstate.New()
	memPipeline := memory.Pipeline{MaxRecords: 12}
	memState := memory.State{}
	lean := leanToolExposure(setup)
	reactRunner := reactruntime.NewRunner(reactruntime.Config{
		Driver:           setup.Driver,
		Tools:            reg,
		Renderer:         renderer,
		LeanToolExposure: lean,
		SystemPrompt: func() string {
			snap := session.Snapshot()
			base := agent.BuildNativeSystemPromptForMode(setup.WorkDir, string(snap.Mode), snap.TaskState != nil)
			if skillText := skills.Describe(loadedSkills); skillText != "" {
				base += "\n\n" + skillText
			}
			if lean {
				if note := deferredToolsNote(reg); note != "" {
					base += "\n\n" + note
				}
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
		ToolExposureObserver: debugToolExposureObserver(setup),
		Progress: func(text string) {
			renderer.Progress(text)
		},
		ConfigureHooks: func(registry *hooks.Registry) {
			if pluginManager != nil {
				pluginManager.RegisterHooks(registry)
			}
		},
		CompactionMaxFailures:    setup.Config.Resilience.CompactionMaxFailures,
		Interactive:              true,
		ToolThrashCircuitBreaker: setup.Config.Resilience.ToolThrashCircuitBreaker,
		OutputStore:              configuredOutputStore(setup.Config),
	})
	registerReactDelegationTools(reg, setup, baseReg, approve, nil, pluginManager, session)

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
		ui := chatstate.ChatUserInput{IsInput: true, Text: input}
		if setup.Config.Chat.AutoSkills == skills.AutoSkillsAuto {
			if skillInput, ok := autoSkillChatInput(loadedSkills, state, input); ok {
				renderer.Info(fmt.Sprintf("skill activated: %s", skillInput.SkillName))
				ui = skillInput
			}
		}
		applySuggestedSkillOverlay(session, input, loadedSkills, state)
		if nudge := suggestedSkillNudge(input, loadedSkills, state); nudge != "" {
			renderer.Info(nudge)
		}
		turnCtx, tc := context.WithCancel(ctx)
		turnCancel.Store(tc)
		err := runChatTurn(turnCtx, reactRunner, ui)
		if err != nil {
			renderer.Error(err.Error())
		}
	}
	fmt.Println()
}

func registerReactDelegationTools(reg *tools.Registry, setup *ChatSetup, baseReg *tools.Registry, approve tools.ApprovalFunc, renderer *agent.EventRenderer, pluginManager *pluginruntime.Manager, sessions ...*reactruntime.Session) {
	if reg == nil || setup == nil || baseReg == nil {
		return
	}
	var parentSession *reactruntime.Session
	if len(sessions) > 0 {
		parentSession = sessions[0]
	}
	var pool *reactruntime.AgentPool
	pool = reactruntime.NewAgentPool(func(ctx context.Context, role, task string) (string, error) {
		driver := setup.Driver
		model := setup.ChatModel
		var systemPrompt func() string
		role = reactruntime.MapSpawnRole(role)

		if agent, ok := pool.GetAgent(role); ok && agent != nil {
			if agent.SystemPrompt != "" {
				systemPrompt = func() string {
					p := agent.SystemPrompt + "\n\nCurrent project: " + setup.WorkDir
					return p
				}
			}
			if agent.Model != "" {
				model = agent.Model
			}
		}
		if driver == nil || model != setup.ChatModel {
			if setup.MakeDriver != nil && strings.TrimSpace(model) != "" {
				driver = setup.MakeDriver(model)
			}
		}
		if driver == nil {
			return "", fmt.Errorf("react delegation driver unavailable")
		}
		if systemPrompt == nil {
			systemPrompt = func() string {
				return agent.BuildNativeSystemPromptForMode(setup.WorkDir, "", false)
			}
		}
		var allowedTools []string
		if agentDef, ok := pool.GetAgent(role); ok && agentDef != nil && len(agentDef.Tools) > 0 {
			allowedTools = append([]string(nil), agentDef.Tools...)
			if agentDef.ReadOnly {
				allowedTools = stripMutationTools(allowedTools)
			}
		}
		childWorkDir := reactruntime.WorkDirFromContext(ctx)
		childBaseReg := baseReg
		if childWorkDir != "" && childWorkDir != setup.WorkDir {
			childBaseReg = newChildAgentRegistry(childWorkDir, nil, baseReg, setup, approve)
		}
		if len(allowedTools) > 0 {
			childBaseReg = childBaseReg.Filter(allowedTools)
		}
		var snap reactruntime.SessionSnapshot
		if parentSession != nil {
			snap = parentSession.Snapshot()
		}
		childTools := childRegistryForRole(childBaseReg, role, snap)
		toolAccessPrompt := childAgentToolAccessPrompt(childTools)
		workDirLabel := setup.WorkDir
		if childWorkDir != "" {
			workDirLabel = childWorkDir
		}
		if systemPrompt != nil {
			origPrompt := systemPrompt
			systemPrompt = func() string {
				return origPrompt() + "\n\n" + toolAccessPrompt + "\n\nWorking directory: " + workDirLabel
			}
		} else {
			systemPrompt = func() string {
				return agent.BuildNativeSystemPromptForMode(workDirLabel, "", false) + "\n\n" + toolAccessPrompt
			}
		}
		childRenderer := agent.NewSilentRenderer(nil)
		if renderer != nil {
			childRenderer = agent.NewSubAgentRenderer(renderer, role)
		}
		if agentID := reactruntime.AgentIDFromContext(ctx); agentID != "" {
			childRenderer = newAgentProgressRenderTarget(childRenderer, func(name, summary string) {
				pool.RecordProgress(agentID, name, summary)
			})
		}
		childRenderer.Info(fmt.Sprintf("[%s] starting", role))
		childRunner := reactruntime.NewRunner(reactruntime.Config{
			Driver:                   driver,
			Tools:                    childTools,
			Renderer:                 childRenderer,
			SystemPrompt:             systemPrompt,
			Session:                  reactruntime.NewSession(),
			ToolExposureObserver:     debugToolExposureObserver(setup),
			CompactionMaxFailures:    setup.Config.Resilience.CompactionMaxFailures,
			Interactive:              false,
			ToolThrashCircuitBreaker: setup.Config.Resilience.ToolThrashCircuitBreaker,
			OutputStore:              configuredOutputStore(setup.Config),
		})
		if err := childRunner.Run(ctx, task); err != nil {
			childRenderer.Info(fmt.Sprintf("[%s] cancelled", role))
			return "", err
		}
		childRenderer.Info(fmt.Sprintf("[%s] done", role))
		return childRunner.LastResponse(), nil
	})
	if parentSession != nil {
		pool.AttachSession(parentSession)
	}
	if setup.debugRec != nil || renderer != nil {
		pool.SetLifecycleObserver(func(state reactruntime.AgentTaskState) {
			if setup.debugRec != nil {
				setup.debugRec.logAgentTask(state)
			}
			if renderer != nil {
				if payload, err := json.Marshal(redactAgentTaskState(state)); err == nil {
					renderer.AgentTaskState(string(payload))
				}
			}
		})
	}
	pool.RegisterAgents(reactruntime.DefaultAgentDefinitions())
	if pluginManager != nil {
		pluginAgents := pluginManager.AgentDefs()
		poolAgents := make([]reactruntime.AgentDefinition, len(pluginAgents))
		for i, a := range pluginAgents {
			poolAgents[i] = reactruntime.AgentDefinition{
				Name:         a.Name,
				Description:  a.Description,
				SystemPrompt: a.SystemPrompt,
				Model:        a.Model,
				Fallbacks:    a.Fallbacks,
				ModelFamily:  a.ModelFamily,
				Tools:        a.Tools,
			}
		}
		pool.RegisterAgents(poolAgents)
	}
	reg.Register(reacttools.NewSpawnAgent(pool))
	reg.Register(reacttools.NewWaitAgent(pool))
	reg.Register(reacttools.NewGetAgentOutput(pool))
	reg.Register(reacttools.NewAgentStatus(pool))
	reg.Register(reacttools.NewKillAgent(pool))
}

func stripMutationTools(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		switch strings.TrimSpace(name) {
		case "write_file", "edit_file", "apply_patch", "artifact_write", "run_command", "exec_session_start", "exec_session_write", "command_write_stdin", "git_commit", "git_push":
			continue
		default:
			out = append(out, name)
		}
	}
	return out
}

func childRegistryForRole(base *tools.Registry, role string, snap reactruntime.SessionSnapshot) *tools.Registry {
	if base == nil {
		return tools.NewRegistry()
	}
	allowed := childReadOnlyToolNames()
	if isChildImplementerRole(role) && snap.SideEffectIntent != nil && len(snap.SideEffectIntent.AllowedPaths) > 0 {
		allowed = append(append([]string(nil), allowed...), childWriteToolNames()...)
	}
	childReg := base.Filter(allowed)
	if isChildImplementerRole(role) && snap.SideEffectIntent != nil && len(snap.SideEffectIntent.AllowedPaths) > 0 {
		childReg = scopeChildMutationRegistry(childReg, snap)
	}
	return childReg
}

func childReadOnlyToolNames() []string {
	return []string{
		"read_file", "read_output", "artifact_read", "list_dir", "search", "code_search", "glob", "view_image",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols",
		"git_status", "git_diff", "git_log", "git_branch_state", "git_merge_status",
		"tool_help", "think",
	}
}

func childWriteToolNames() []string {
	return []string{
		"write_file", "edit_file", "apply_patch", "artifact_write",
	}
}

func isChildImplementerRole(role string) bool {
	normalized := strings.ToLower(strings.TrimSpace(reactruntime.MapSpawnRole(role)))
	normalized = strings.NewReplacer("-", " ", "_", " ").Replace(normalized)
	fields := strings.Fields(normalized)
	for _, field := range fields {
		switch field {
		case "research", "audit", "auditor", "explore", "explorer", "review", "reviewer":
			return false
		}
	}
	for _, field := range fields {
		switch field {
		case "worker", "implementer", "implementation", "developer", "coder":
			return true
		}
	}
	return false
}

func scopeChildMutationRegistry(reg *tools.Registry, snap reactruntime.SessionSnapshot) *tools.Registry {
	if reg == nil {
		return reg
	}
	scoped := tools.NewRegistry()
	for _, tool := range reg.All() {
		scoped.Register(scopeChildMutationTool(tool, snap))
	}
	return scoped
}

func scopeChildMutationTool(tool tools.Tool, snap reactruntime.SessionSnapshot) tools.Tool {
	origExecute := tool.Execute
	if origExecute == nil {
		return tool
	}
	switch tool.Name {
	case "write_file", "edit_file", "artifact_write":
		tool.Execute = func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			if !childMutationPathAllowed(path, snap) {
				return fmt.Sprintf("blocked: child write outside delegated side-effect paths: %s", strings.TrimSpace(path)), nil
			}
			return origExecute(ctx, args)
		}
	case "apply_patch":
		tool.Execute = func(ctx context.Context, args map[string]any) (string, error) {
			patch, _ := args["patch"].(string)
			paths := childPatchMutationPaths(patch)
			if len(paths) == 0 {
				return "blocked: child patch has no scoped file paths", nil
			}
			for _, path := range paths {
				if !childMutationPathAllowed(path, snap) {
					return fmt.Sprintf("blocked: child write outside delegated side-effect paths: %s", strings.TrimSpace(path)), nil
				}
			}
			return origExecute(ctx, args)
		}
	}
	return tool
}

func childMutationPathAllowed(path string, snap reactruntime.SessionSnapshot) bool {
	intent := snap.SideEffectIntent
	if intent == nil {
		return false
	}
	target := childNormalizeMutationPath(path, snap)
	if target == "" {
		return false
	}
	for _, allowed := range append(append([]string(nil), intent.AllowedPaths...), intent.ArtifactPaths...) {
		if childNormalizeMutationPath(allowed, snap) == target {
			return true
		}
	}
	return false
}

func childNormalizeMutationPath(path string, snap reactruntime.SessionSnapshot) string {
	path = strings.Trim(path, " `\t\r\n\"'")
	if path == "" || path == "/dev/null" {
		return ""
	}
	if filepath.IsAbs(path) {
		for _, root := range []string{snap.ActiveWorkspaceRoot, sideEffectWorkspaceRoot(snap.SideEffectIntent)} {
			root = strings.TrimSpace(root)
			if root == "" {
				continue
			}
			rel, err := filepath.Rel(root, path)
			if err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
				return filepath.ToSlash(filepath.Clean(rel))
			}
		}
		return ""
	}
	cleaned := filepath.Clean(filepath.FromSlash(path))
	if cleaned == "." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
		return ""
	}
	return filepath.ToSlash(cleaned)
}

func sideEffectWorkspaceRoot(intent *reactruntime.SideEffectIntent) string {
	if intent == nil {
		return ""
	}
	return intent.WorkspaceRoot
}

func childPatchMutationPaths(patch string) []string {
	var paths []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "diff --git "):
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				paths = append(paths, strings.TrimPrefix(parts[2], "a/"), strings.TrimPrefix(parts[3], "b/"))
			}
		case strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ "):
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				paths = append(paths, strings.TrimPrefix(strings.TrimPrefix(parts[1], "a/"), "b/"))
			}
		}
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path != "" && path != "/dev/null" && !slices.Contains(out, path) {
			out = append(out, path)
		}
	}
	return out
}

func childAgentToolAccessPrompt(reg *tools.Registry) string {
	if reg == nil {
		return "You have repository access through native tools. Do not tell the user to run git commands because you lack access; use the available tools or report the exact tool/path failure."
	}
	names := make([]string, 0, len(reg.All()))
	for _, tool := range reg.All() {
		if strings.TrimSpace(tool.Name) != "" {
			names = append(names, strings.TrimSpace(tool.Name))
		}
	}
	sort.Strings(names)
	return strings.Join([]string{
		"You have repository access through these native tools: " + strings.Join(names, ", ") + ".",
		"Use read_file/list_dir/search/code_search/glob for repository inspection and git_status/git_diff/git_log/git_branch_state for Git state.",
		"Do not tell the user to run git commands because you lack access; use the available native tools or report the exact tool/path failure.",
		"Child agents must not commit or push. Return findings and proposed artifact content. Parent/orchestrator owns write, commit, push, and verification gates.",
		reactruntime.AgentHandoffInstructions(),
	}, "\n")
}

// newChildAgentRegistry creates a registry for a child agent whose working
// directory differs from the parent.  Tools that depend on a local working
// directory (read, write, search, git, etc.) are re-created pointing at
// childWorkDir.  Non-directory tools are copied from parentReg.
func newChildAgentRegistry(childWorkDir string, allowedTools []string, parentReg *tools.Registry, setup *ChatSetup, approve tools.ApprovalFunc) *tools.Registry {
	if childWorkDir == "" || setup == nil || setup.Config == nil {
		return parentReg.Filter(allowedTools)
	}
	cfg := setup.Config
	secretPolicy := tools.SecretPolicy{
		Read:           tools.SecretPolicyMode(cfg.Security.Secrets.Read),
		Write:          tools.SecretPolicyMode(cfg.Security.Secrets.Write),
		CommandOutput:  tools.SecretPolicyMode(cfg.Security.Secrets.CommandOutput),
		ApprovalDetail: tools.SecretPolicyMode(cfg.Security.Secrets.ApprovalDetail),
	}
	childReg := tools.NewRegistry()
	childReg.Register(tools.NewReadFile(childWorkDir, secretPolicy))
	childReg.Register(tools.NewListDir(childWorkDir, cfg.Chat.IgnoreDirs))
	childReg.Register(tools.NewSearch(childWorkDir))
	childReg.Register(tools.NewCodeSearch(childWorkDir))
	childReg.Register(tools.NewGlob(childWorkDir, cfg.Chat.IgnoreDirs))
	childReg.Register(tools.NewViewImage(childWorkDir))
	childReg.Register(tools.NewLSPDefinition(childWorkDir))
	childReg.Register(tools.NewLSPReferences(childWorkDir))
	childReg.Register(tools.NewLSPHover(childWorkDir))
	childReg.Register(tools.NewLSPDocumentSymbols(childWorkDir))
	childReg.Register(tools.NewGitStatus(childWorkDir))
	childReg.Register(tools.NewGitDiff(childWorkDir))
	childReg.Register(tools.NewGitLog(childWorkDir))
	childReg.Register(tools.NewGitBranchState(childWorkDir))
	childReg.Register(tools.NewGitMergeStatus(childWorkDir))
	childReg.Register(tools.NewToolHelp(childReg))
	childReg.Register(tools.NewThink())

	for _, tool := range parentReg.All() {
		if _, exists := childReg.Get(tool.Name); !exists {
			childReg.Register(tool)
		}
	}
	return childReg.Filter(allowedTools)
}

type agentProgressRenderTarget struct {
	target agent.RenderTarget
	record func(name, summary string)
}

func newAgentProgressRenderTarget(target agent.RenderTarget, record func(name, summary string)) agent.RenderTarget {
	if target == nil || record == nil {
		return target
	}
	return agentProgressRenderTarget{target: target, record: record}
}

func (r agentProgressRenderTarget) AgentToken(text string) { r.target.AgentToken(text) }
func (r agentProgressRenderTarget) AgentText(text string)  { r.target.AgentText(text) }
func (r agentProgressRenderTarget) ToolCall(name, summary string) {
	r.record(name, summary)
	r.target.ToolCall(name, summary)
}
func (r agentProgressRenderTarget) ToolResult(name, output, diff string, isError bool) {
	r.target.ToolResult(name, output, diff, isError)
}
func (r agentProgressRenderTarget) Stats(duration time.Duration, usage llm.Usage) {
	r.target.Stats(duration, usage)
}
func (r agentProgressRenderTarget) StatsWithContext(duration time.Duration, usage llm.Usage, contextUsed int) {
	if target, ok := r.target.(agent.ContextStatsTarget); ok {
		target.StatsWithContext(duration, usage, contextUsed)
		return
	}
	r.target.Stats(duration, usage)
}
func (r agentProgressRenderTarget) Error(msg string) { r.target.Error(msg) }
func (r agentProgressRenderTarget) Info(msg string)  { r.target.Info(msg) }

func resolveChatRuntimeMode() chatRuntimeMode {
	return chatRuntimeReact
}

type chatTurnRunner interface {
	Run(context.Context, string) error
	RunWithParts(context.Context, string, []llm.MessageContentPart) error
	AppendSkillContext(name, body string)
	EmitResponse(string)
	SetTaskState(reactruntime.TaskState)
	TaskState() *reactruntime.TaskState
	QueuePendingInput(string)
	DiscardPendingInput() []string
	MarkInterrupted()
}

const promptBoundaryRefusal = "I can't provide hidden system/developer prompts or internal instructions, including paraphrased or hypothetical versions. I can summarize my role and high-level guardrails if useful."

func runChatTurn(ctx context.Context, reactRunner chatTurnRunner, input chatstate.ChatUserInput) error {
	text := input.Text
	if strings.TrimSpace(input.SkillName) != "" || strings.TrimSpace(input.SkillBody) != "" {
		reactRunner.AppendSkillContext(input.SkillName, input.SkillBody)
		if strings.TrimSpace(text) == "" {
			return nil
		}
	}
	if reactRunner == nil {
		return fmt.Errorf("chat react runner is nil")
	}
	parts := chatInputToContentParts(input.Attachments)
	if len(parts) > 0 {
		return reactRunner.RunWithParts(ctx, text, parts)
	}
	return reactRunner.Run(ctx, text)
}

func autoSkillChatInput(loadedSkills []skills.Skill, state *chatstate.State, input string) (chatstate.ChatUserInput, bool) {
	s, ok := skills.DetectAuto(loadedSkills, input)
	if !ok {
		return chatstate.ChatUserInput{}, false
	}
	if state != nil {
		state.ActivateSkill(s.Name)
	}
	return chatstate.ChatUserInput{IsInput: true, Text: input, SkillName: s.Name, SkillBody: s.Body}, true
}

func chatInputToContentParts(attachments []chatstate.ChatAttachment) []llm.MessageContentPart {
	if len(attachments) == 0 {
		return nil
	}
	parts := make([]llm.MessageContentPart, 0, len(attachments))
	for _, att := range attachments {
		parts = append(parts, llm.MessageContentPart{
			Type: "image",
			Image: &llm.ImageContent{
				Path:     att.Path,
				MIMEType: att.MIMEType,
				Width:    att.Width,
				Height:   att.Height,
			},
		})
	}
	return parts
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

type chatSessionControl interface {
	SetDriver(llm.Driver)
	ClearHistory()
	AppendUserMessage(string)
	AppendSkillContext(name, body string)
	EmitResponse(string)
	SetTaskState(reactruntime.TaskState)
	CompactHistory(keep int) bool
	CompactionStatus() string
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
	case input == "/clear" || input == "/new":
		if state != nil {
			state.Clear()
		}
		if session != nil {
			session.ClearHistory()
		}
		if input == "/new" {
			renderer.Info("new session started")
		} else {
			renderer.Info("conversation history cleared")
		}
	case input == "/compact" || strings.HasPrefix(input, "/compact "):
		if session == nil {
			renderer.Error("compact unavailable")
			return true
		}
		arg := strings.TrimSpace(strings.TrimPrefix(input, "/compact"))
		switch {
		case arg == "":
			if session.CompactHistory(1) {
				renderer.Info("compacted conversation history")
			} else {
				renderer.Info("conversation history already compact")
			}
		case arg == "status":
			renderer.Info(session.CompactionStatus())
		case strings.HasPrefix(arg, "recent "):
			keep, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(arg, "recent ")))
			if err != nil || keep < 1 {
				renderer.Error("usage: /compact recent N")
				return true
			}
			if session.CompactHistory(keep) {
				renderer.Info(fmt.Sprintf("compacted conversation history; preserved recent %d turns", keep))
			} else {
				renderer.Info("conversation history already compact")
			}
		default:
			renderer.Error("usage: /compact, /compact recent N, or /compact status")
		}
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
				session.AppendSkillContext(s.Name, s.Body)
			}
			renderer.Info(fmt.Sprintf("skill activated: %s", s.Name))
			return true
		}
		renderer.Error(fmt.Sprintf("unknown command: %s (try /help)", input))
	}
	return true
}

func ContainsModel(models []string, name string) bool {
	return slices.Contains(models, name)
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
	fmt.Println("    /new            start a clean session")
	fmt.Println("    /clear          clear conversation history")
	fmt.Println("    /compact        compact conversation history")
	fmt.Println("    /compact recent N  compact while preserving recent N turns")
	fmt.Println("    /compact status show compaction status")
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
