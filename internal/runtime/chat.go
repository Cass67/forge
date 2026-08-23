package runtime

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"forge/internal/lsp"
	"forge/internal/mcp"
	"forge/internal/memory"
	"forge/internal/modelcatalog"
	"forge/internal/permissions"
	"forge/internal/plugin"
	pluginruntime "forge/internal/plugins"
	"forge/internal/protocol"
	reactruntime "forge/internal/react"
	reacttools "forge/internal/react/tools"
	"forge/internal/sessionstore"
	"forge/internal/skills"
	"forge/internal/tui"
)

var (
	loadChatConfig    = bootstrap.LoadConfig
	loadChatTokens    = bootstrap.LoadTokens
	saveLastChatModel = config.SaveChatLastModel
	defaultConfigPath = config.DefaultPath
	runChatLiveUI     = tui.RunChatLiveBubbleTea
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

// applyChatParams pushes the currently selected generation params (reasoning
// effort) onto a driver. Temperature stays at the provider default (-1). Called
// after every MakeDriver so the effort survives model/provider switches.
func (s *ChatSetup) applyChatParams(d llm.Driver) {
	if s == nil {
		return
	}
	if c, ok := d.(llm.Configurable); ok {
		c.SetParams(llm.Params{Temperature: -1, ReasoningEffort: s.effectiveReasoningEffort()})
	}
}

// effectiveReasoningEffort is the level actually sent to the provider. With no
// level chosen it falls back to the lowest the model advertises: sending none
// meant a reasoning-capable model never reasoned, so its thinking could never
// be displayed however the renderer was configured. "none" opts out.
func (s *ChatSetup) effectiveReasoningEffort() string {
	if s == nil {
		return ""
	}
	chosen := strings.TrimSpace(s.ReasoningEffort)
	if strings.EqualFold(chosen, "none") {
		return ""
	}
	if s.reasoningEffortChosen || chosen != "" {
		return chosen
	}
	ref := bootstrap.ParseModelRef(s.ChatModel)
	info := modelcatalog.Lookup(ref.Provider, ref.Model)
	if info == nil {
		return ""
	}
	return lowestReasoningEffort(info.ReasoningEfforts)
}

// lowestReasoningEffort picks the cheapest advertised level, preferring the
// conventional names before falling back to the first the provider lists.
func lowestReasoningEffort(efforts []string) string {
	for _, want := range []string{"minimal", "none", "low"} {
		for _, have := range efforts {
			if strings.EqualFold(have, want) {
				if strings.EqualFold(have, "none") {
					return ""
				}
				return have
			}
		}
	}
	if len(efforts) > 0 {
		return efforts[0]
	}
	return ""
}

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
	// ReasoningEffort is the currently selected provider reasoning-effort level,
	// re-applied to each driver built via MakeDriver so it survives model switches.
	ReasoningEffort string
	// reasoningEffortChosen records that the level came from configuration or
	// from the user, so the advertised default no longer applies.
	reasoningEffortChosen bool
	// DroppedModel is the saved chat model that was discarded at startup
	// because no provider currently offers it.
	DroppedModel string
	// ResumeThreadID, when set, seeds the session with a stored thread's
	// history before the first turn (forge --resume / --continue).
	ResumeThreadID string
	// LiveRunner, when set, renders the live chat instead of the terminal UI.
	// forge-gui sets it to drive a native Wails window; leaving it nil keeps
	// the Bubble Tea surface. Declared as a function so this package stays
	// free of any GUI dependency.
	LiveRunner LiveRunner
	debugRec   *chatDebugRecorder
}

// inferMCPType names a server's transport, falling back to its shape when the
// config does not say.
func inferMCPType(server config.MCPServerConfig) string {
	if t := strings.TrimSpace(server.Type); t != "" {
		return t
	}
	if strings.TrimSpace(server.URL) != "" {
		return "remote"
	}
	return "stdio"
}

// LiveRunner renders the live chat event stream. It has the same contract as
// tui.RunChatLiveBubbleTea: it blocks until the surface is closed.
type LiveRunner func(events <-chan llm.Event, cfg tui.ChatLiveConfig, inputCh chan<- string, doneCh <-chan struct{}) tui.ChatLiveResult

// ResolveResumeThreadID maps CLI resume flags to a stored thread ID. An
// explicit id is returned as-is; continueLast picks the most recently updated
// thread. Returns "" when nothing matches (caller decides whether to error).
func ResolveResumeThreadID(cfg *config.Config, explicitID string, continueLast bool) (string, error) {
	if id := strings.TrimSpace(explicitID); id != "" {
		return id, nil
	}
	if !continueLast {
		return "", nil
	}
	outputDir := ""
	if cfg != nil {
		outputDir = cfg.ResolvedOutputDir()
	}
	if outputDir == "" {
		return "", fmt.Errorf("session output dir not configured")
	}
	store := sessionstore.NewJSONLThreadStore(filepath.Join(outputDir, "threads"))
	records, err := store.ListThreads(context.Background(), sessionstore.ListOptions{Limit: 1})
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", fmt.Errorf("no previous sessions to continue")
	}
	return records[0].ThreadID, nil
}

// ResolveWorkspaceResumeThreadID returns newest non-empty thread belonging to
// workDir. GUI uses this for cold-start continuity; CLI keeps explicit resume
// semantics above.
func ResolveWorkspaceResumeThreadID(cfg *config.Config, workDir string) (string, error) {
	if cfg == nil || strings.TrimSpace(cfg.ResolvedOutputDir()) == "" {
		return "", nil
	}
	want, err := filepath.Abs(strings.TrimSpace(workDir))
	if err != nil {
		return "", err
	}
	store := sessionstore.NewJSONLThreadStore(filepath.Join(cfg.ResolvedOutputDir(), "threads"))
	records, err := store.ListThreads(context.Background(), sessionstore.ListOptions{Limit: 500})
	if err != nil {
		return "", err
	}
	for _, record := range records {
		cwd := strings.TrimSpace(record.Metadata.CWD)
		if cwd == "" {
			continue
		}
		got, absErr := filepath.Abs(cwd)
		if absErr != nil || got != want {
			continue
		}
		items, readErr := store.ReadItems(context.Background(), record.ThreadID)
		if readErr != nil {
			return "", readErr
		}
		for _, item := range items {
			if item.Kind == protocol.ItemUserMessage && item.Message != nil && strings.TrimSpace(item.Message.Text) != "" {
				return record.ThreadID, nil
			}
		}
	}
	return "", nil
}

// adoptResumeThread seeds session with a stored thread's history. It is a no-op
// when setup.ResumeThreadID is empty. Errors are reported to stderr and the
// session starts fresh rather than aborting the launch.
func adoptResumeThread(setup *ChatSetup, session *reactruntime.Session) {
	threadID := strings.TrimSpace(setup.ResumeThreadID)
	if threadID == "" || session == nil {
		return
	}
	outputDir := setup.Config.ResolvedOutputDir()
	if outputDir == "" {
		fmt.Fprintln(os.Stderr, "resume: session output dir not configured; starting fresh")
		return
	}
	store := sessionstore.NewJSONLThreadStore(filepath.Join(outputDir, "threads"))
	items, err := store.ReadItems(context.Background(), threadID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resume: %v; starting fresh\n", err)
		return
	}
	if len(items) == 0 {
		fmt.Fprintf(os.Stderr, "resume: thread %s not found; starting fresh\n", threadID)
		return
	}
	n, err := session.AdoptReplayItems(items)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resume: %v; starting fresh\n", err)
		return
	}
	session.SetDurableThreadID(threadID)
	fmt.Fprintf(os.Stderr, "resumed thread %s (%d turns)\n", threadID, n)
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
	}
	droppedModel := ""
	if modelOverride == "" && chatModel != "" && !ContainsModel(available, chatModel) {
		droppedModel = chatModel
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
			return nil, errors.New(bootstrap.DriverUnavailableReason(cfg, authTokens, chatModel))
		}
		persistChatLastModel(cfg, chatModel)
	}

	return &ChatSetup{
		Config:       cfg,
		ChatModel:    chatModel,
		WorkDir:      absWd,
		Driver:       driver,
		Yolo:         yolo,
		Available:    available,
		Providers:    providers,
		MakeDriver:   makeChatDriver,
		DroppedModel: droppedModel,

		ReasoningEffort:       strings.TrimSpace(cfg.Chat.ReasoningEffort),
		reasoningEffortChosen: strings.TrimSpace(cfg.Chat.ReasoningEffort) != "",
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

func registerTools(reg *tools.Registry, workDir string, cfg *config.Config, session *reactruntime.Session, approve tools.ApprovalFunc, notify func(string), emitCommandStatus func(tools.ExecSessionStatus), forcePrompt ...tools.ApprovalFunc) (*tools.PreviewRuntime, *mcp.Manager, *tools.ExecSessionManager) {
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
	registerMCPTools := func(defs []mcp.Tool) {
		for _, def := range defs {
			reg.Register(tools.NewMCPDynamicTool(def, mcpManager))
		}
	}
	mcpManager.SetEventHandler(func(ev mcp.Event) {
		switch ev.Kind {
		case mcp.EventToolsChanged, mcp.EventResourcesChanged:
			registerMCPTools(ev.Snapshot.Tools)
			if notify != nil {
				notify(fmt.Sprintf("%s (%s)", strings.TrimSpace(ev.Message), ev.ServerName))
			}
		case mcp.EventRefreshed:
			registerMCPTools(ev.Snapshot.Tools)
		case mcp.EventResourceUpdated:
			if notify != nil {
				notify(fmt.Sprintf("MCP resource updated on %s: %s", ev.ServerName, ev.URI))
			}
		case mcp.EventLogMessage, mcp.EventProgress:
			if notify != nil {
				notify(fmt.Sprintf("%s (%s)", strings.TrimSpace(ev.Message), ev.ServerName))
			}
		default:
		}
	})
	// Connecting to MCP servers is a network round trip per server, and it used
	// to run before the chat UI painted: a single remote server cost more than a
	// second of blank terminal. Tools register themselves as servers land.
	registerMCPTools(mcpManager.Tools())
	go func() {
		_ = mcpManager.Refresh(context.Background(), cfg)
		registerMCPTools(mcpManager.Tools())
		if notify != nil {
			notify(mcpStartupStatus(mcpManager, cfg))
		}
	}()
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
	if tools.AstGrepAvailable() {
		reg.Register(tools.NewAstGrep(workDir, secretPolicy))
		reg.Register(tools.NewAstEdit(workDir, approve, secretPolicy))
	}
	reg.Register(tools.NewLSPDefinition(workDir))
	reg.Register(tools.NewLSPReferences(workDir))
	reg.Register(tools.NewLSPHover(workDir))
	reg.Register(tools.NewLSPDocumentSymbols(workDir))
	reg.Register(tools.WithSandboxExecutor(tools.NewRunCommandWithWorkDirProvider(workDir, workDirProvider, cfg.Chat.CommandTimeout, execManager, approve, secretPolicy, fp), approve, workDirProvider, workDir, secretPolicy))
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
	reg.Register(tools.NewGitShowWithWorkDirProvider(workDir, workDirProvider))
	reg.Register(tools.NewGitBlameWithWorkDirProvider(workDir, workDirProvider))
	reg.Register(tools.NewGHPullRequestWithWorkDirProvider(workDir, workDirProvider))
	reg.Register(tools.NewReviewDiffWithWorkDirProvider(workDir, workDirProvider))
	reg.Register(tools.NewReportFindings())
	reg.Register(tools.NewViewImage(workDir))
	reg.Register(tools.NewToolHelp(reg))
	reg.Register(reacttools.NewUpdatePlan(session))
	reg.Register(reacttools.NewEnterPlanMode(session))
	reg.Register(reacttools.NewExitPlanMode(session))
	reg.Register(reacttools.NewAskUserQuestion())
	// Keyed off configuration rather than live sessions: the connect now happens
	// in the background, so no server has reported in yet.
	if len(mcpManager.EnabledServers(cfg)) > 0 {
		reg.Register(tools.NewListMCPResources(mcpManager))
		reg.Register(tools.NewListMCPResourceTemplates(mcpManager))
		reg.Register(tools.NewReadMCPResource(mcpManager))
	}
	reg.Register(tools.NewGitCommitWithWorkDirProvider(workDir, workDirProvider, approve))
	reg.Register(tools.NewGitPushWithWorkDirProvider(workDir, workDirProvider, approve))
	reg.Register(tools.NewGitWorktreeWithWorkDirProvider(workDir, workDirProvider, approve))
	// Web access stays visible in the prompt. Hiding it behind tool_help meant
	// models concluded they had no network at all and answered research
	// questions from memory, flagging every fact as unverifiable, rather than
	// looking anything up. Neither tool needs a credential.
	reg.Register(tools.NewWebFetch())
	reg.Register(tools.NewWebSearch())
	return previewRuntime, mcpManager, execManager
}

func mcpStartupStatus(manager *mcp.Manager, cfg *config.Config) string {
	enabled := manager.EnabledServers(cfg)
	if len(enabled) == 0 {
		return "MCP: no servers configured"
	}

	connected := manager.ConnectedServers()
	connectedSet := make(map[string]bool, len(connected))
	for _, name := range connected {
		connectedSet[name] = true
	}
	reasons := make(map[string]string, len(enabled))
	for _, status := range manager.Status() {
		reasons[status.Name] = status.Error
	}
	failed := make([]string, 0, len(enabled)-len(connected))
	for _, server := range enabled {
		if connectedSet[server.Name] {
			continue
		}
		// Without the reason the user only learns a server is missing, not
		// that it needs a token, a longer timeout, or an executable on PATH.
		if reason := strings.TrimSpace(reasons[server.Name]); reason != "" {
			failed = append(failed, fmt.Sprintf("%s (%s)", server.Name, reason))
			continue
		}
		failed = append(failed, server.Name)
	}

	parts := make([]string, 0, 2)
	if len(connected) > 0 {
		parts = append(parts, fmt.Sprintf("loaded %s (%d tools)", strings.Join(connected, ", "), len(manager.Tools())))
	}
	if len(failed) > 0 {
		parts = append(parts, "failed "+strings.Join(failed, ", "))
	}
	return "MCP: " + strings.Join(parts, "; ")
}

type lazyDurableSessionSink struct {
	mu          sync.Mutex
	live        *sessionstore.LiveSession
	metadata    sessionstore.ThreadMetadataPatch
	sessionMeta protocol.Item
	initialized bool
}

func (s *lazyDurableSessionSink) Append(ctx context.Context, item protocol.Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		if err := s.live.UpdateMetadata(ctx, s.metadata); err != nil {
			return err
		}
		if err := s.live.Append(ctx, s.sessionMeta); err != nil {
			return err
		}
		s.initialized = true
	}
	return s.live.Append(ctx, item)
}

func configureDurableSessionSink(cfg *config.Config, session *reactruntime.Session, workDir string) {
	if cfg == nil || session == nil {
		return
	}
	store := sessionstore.NewJSONLThreadStore(filepath.Join(cfg.ResolvedOutputDir(), "threads"))
	model := strings.TrimSpace(cfg.Chat.Model)
	if model == "" {
		model = strings.TrimSpace(cfg.Chat.LastModel)
	}
	threadID := strings.TrimSpace(session.DurableThreadID())
	if threadID == "" {
		threadID = durableThreadID(session)
	}
	session.SetDurableThreadID(threadID)
	live := sessionstore.NewLiveSession(threadID, store, sessionstore.DefaultPersistencePolicy())
	session.SetDurableSink(&lazyDurableSessionSink{
		live: live,
		metadata: sessionstore.ThreadMetadataPatch{
			Title:     "Forge chat",
			Preview:   strings.TrimSpace(session.Snapshot().InitialInput),
			CWD:       strings.TrimSpace(workDir),
			Model:     model,
			UpdatedAt: time.Now().UTC(),
		},
		sessionMeta: protocol.Item{
			Kind:        protocol.ItemSessionMeta,
			SessionMeta: &protocol.SessionMetaItem{Source: "runtime", CWD: strings.TrimSpace(workDir), Model: model},
		},
	})
}

// backgroundExitNote renders a concise system note about a finished background
// command session so the runner surfaces it to the model on the next turn
// instead of the model having to poll command_status.
func backgroundExitNote(status tools.ExecSessionStatus) string {
	tail := strings.TrimSpace(status.Output)
	if len(tail) > 2000 {
		tail = "…" + tail[len(tail)-2000:]
	}
	note := fmt.Sprintf("[background command session %d exited with code %d: %s]",
		status.SessionID, status.ExitCode, strings.TrimSpace(status.Command))
	if tail != "" {
		note += "\nfinal output:\n" + tail
	}
	return note
}

func configuredOutputStore(cfg *config.Config) sessionstore.OutputStore {
	if cfg == nil {
		return nil
	}
	return sessionstore.NewFileOutputStore(cfg.ResolvedOutputDir())
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
	// Auto-discover plugins from ~/.forge/plugins/
	if cfg != nil {
		discovered, err := pluginruntime.ScanPluginsDir("")
		if err != nil && notify != nil {
			notify("plugin scan: " + err.Error())
		}
		if len(discovered) > 0 {
			added, _, _ := pluginruntime.MergeDiscovered(cfg, discovered)
			if added > 0 && notify != nil {
				notify(fmt.Sprintf("auto-discovered %d plugin(s)", added))
			}
		}
	}
	hasConfigPlugins := cfg != nil && len(cfg.Plugins) > 0
	if !hasConfigPlugins && !pluginruntime.HasNativePlugins() {
		return nil
	}
	manager := pluginruntime.NewManager(workDir, cfg.Plugins)
	if hasConfigPlugins {
		if err := manager.Start(context.Background()); err != nil && notify != nil {
			notify("plugin service: " + err.Error())
		}
	}
	manager.CollectNativePlugins()
	if !manager.HasPlugins() {
		_ = manager.Close()
		return nil
	}
	return manager
}

func reloadPluginsHandler(existing **pluginruntime.Manager, cfg *config.Config, workDir string, reg *tools.Registry, approve tools.ApprovalFunc, notify func(string)) string {
	// Re-read config from disk
	freshCfg, err := config.Load(config.DefaultPath())
	if err != nil {
		if notify != nil {
			notify("reload: " + err.Error())
		}
		return fmt.Sprintf("reload failed: config read error: %v", err)
	}

	// Scan for auto-discovered plugins in ~/.forge/plugins/
	discovered, err := pluginruntime.ScanPluginsDir("")
	if err != nil && notify != nil {
		notify("reload: scan: " + err.Error())
	}
	added, _, updated := pluginruntime.MergeDiscovered(freshCfg, discovered)

	// Update the live config in place
	if cfg != nil {
		cfg.Plugins = freshCfg.Plugins
	}

	// Kill old plugin manager
	if *existing != nil {
		_ = (*existing).Close()
	}

	// Start new plugin manager with updated configs
	newManager := pluginruntime.NewManager(workDir, freshCfg.Plugins)
	if len(freshCfg.Plugins) > 0 {
		if err := newManager.Start(context.Background()); err != nil && notify != nil {
			notify("reload: " + err.Error())
		}
	}
	newManager.CollectNativePlugins()

	if newManager.HasPlugins() {
		newManager.RegisterTools(reg, approve)
	}

	*existing = newManager

	parts := make([]string, 0, 3)
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d plugin(s) added", added))
	}
	if updated > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", updated))
	}
	if len(discovered) > 0 && added == 0 && updated == 0 {
		parts = append(parts, "all plugins up to date")
	}
	if len(parts) == 0 {
		if len(discovered) > 0 {
			return fmt.Sprintf("reload: %d auto-discovered plugin(s) unchanged", len(discovered))
		}
		return "reload: no changes detected"
	}
	return "reload: " + strings.Join(parts, ", ")
}

func RunChatLive(setup *ChatSetup) {
	// The initial driver was never given generation params: they were only
	// applied on a model switch or an /effort change, so a configured effort
	// did nothing until one of those happened, and a model that only reasons
	// when asked never reasoned at all.
	if setup != nil {
		setup.applyChatParams(setup.Driver)
	}
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
	if setup.DroppedModel != "" {
		evRenderer.Info(fmt.Sprintf(
			"saved model %q is no longer available — no model selected; pick one with the model switcher",
			setup.DroppedModel))
	}
	session := reactruntime.NewSession()
	session.SetActiveWorkspaceRoot(setup.WorkDir)
	adoptResumeThread(setup, session)

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
	// Yolo is a live toggle rather than a start-up flag: the prompt consults
	// it per action, so it can be flipped mid-session from the UI.
	var yoloOn atomic.Bool
	yoloOn.Store(setup.Yolo)
	livePrompt := evRenderer.LiveApproval()
	gate.SetPrompt(func(action tools.Action) (bool, error) {
		if yoloOn.Load() {
			return true, nil
		}
		return livePrompt(action)
	})
	approve = gate.Approve

	reg := tools.NewRegistry()
	previewRuntime, mcpManager, execManager := registerTools(reg, setup.WorkDir, setup.Config, session, approve, evRenderer.Info, func(status tools.ExecSessionStatus) {
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
	if execManager != nil {
		defer execManager.Close()
	}
	lsp.Shared().SetServers(lsp.ServersFromConfig(setup.Config.LSP))
	// Language servers are pooled process-wide, so nothing else releases them
	// when a chat ends. Left running they strand a warm gopls or rust-analyzer
	// per workspace; the pool respawns on the next call.
	defer lsp.Shared().Close(context.Background())
	pluginManager := startChatPluginManager(setup.Config, setup.WorkDir, evRenderer.Info)
	if pluginManager != nil {
		defer func() { _ = pluginManager.Close() }()
		pluginManager.RegisterTools(reg, approve)
	}
	reloadPlugins := func() string {
		return reloadPluginsHandler(&pluginManager, setup.Config, setup.WorkDir, reg, approve, evRenderer.Info)
	}
	baseReg := reg.Filter(nil)
	loadedSkills := skills.Load(setup.WorkDir)
	state := chatstate.New()
	memPipeline := memory.Pipeline{MaxRecords: 12}
	memState := memory.LoadState(setup.WorkDir)
	if memState.Summary != "" {
		session.SetMemorySummary(memState.Summary)
	}
	var memMu sync.Mutex
	remember := func(text string) bool {
		memMu.Lock()
		defer memMu.Unlock()
		next, ok := memPipeline.Remember(memState, text)
		if !ok {
			return false
		}
		memState = next
		session.SetMemorySummary(next.Summary)
		_ = memory.SaveState(setup.WorkDir, next)
		return true
	}
	lean := leanToolExposure(setup)
	reactRunner := reactruntime.NewRunner(reactruntime.Config{
		Driver:           setup.Driver,
		Tools:            reg,
		Renderer:         evRenderer,
		ShowReasoning:    setup.Config.Chat.ReasoningVisible(),
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
			memMu.Lock()
			defer memMu.Unlock()
			if next, ok := memPipeline.Process(memState, snapshot); ok {
				memState = next
				session.SetMemorySummary(next.Summary)
				_ = memory.SaveState(setup.WorkDir, next)
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
		ContextWindowTokens:      func() int { return lookupContextWindow(setup.ChatModel) },
		DiagnosticsProvider:      lspDiagnosticsProvider(setup.WorkDir),
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
			case "__new_session__":
				if setup != nil && setup.debugRec != nil {
					setup.debugRec.logInput("control", rawInput)
				}
				// Cancel any in-flight turn first, then clear, so a new session
				// never inherits the old run's history or queued steering.
				if running {
					reactRunner.MarkInterrupted()
					if tc, ok := turnCancel.Load().(context.CancelFunc); ok {
						tc()
					}
					_ = reactRunner.DiscardPendingInput()
					running = false
				}
				state.Clear()
				reactRunner.ClearHistory()
				// New chat must stop owning old durable thread. Otherwise sidebar
				// shows blank chat while delete guard still protects old selection.
				session.SetDurableThreadID("")
				configureDurableSessionSink(setup.Config, session, setup.WorkDir)
				evRenderer.Info("new session started")
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
		RoleModel: func(role string) string {
			cfg, _ := refreshChatSetupState(setup)
			if cfg == nil {
				return ""
			}
			return cfg.RoleModel(role)
		},
		ModelInfo: func(model string) *modelcatalog.ModelInfo {
			ref := bootstrap.ParseModelRef(model)
			return modelcatalog.Lookup(ref.Provider, ref.Model)
		},
		DescribeModel: func(model string) string {
			cfg, authTokens := refreshChatSetupState(setup)
			return bootstrap.ModelDisplayLabel(cfg, authTokens, model)
		},
		// Complete builds its own driver rather than borrowing the chat
		// driver: several of these are stateful, and a side call must not
		// disturb the conversation the user is having.
		Complete: func(ctx context.Context, model, system, user string) (string, error) {
			cfg, authTokens := refreshChatSetupState(setup)
			if strings.TrimSpace(model) == "" {
				model = setup.ChatModel
			}
			driver := bootstrap.DriverForModel(cfg, authTokens, model)
			if driver == nil {
				return "", fmt.Errorf("no provider available for %s", model)
			}
			messages := make([]llm.Message, 0, 2)
			if strings.TrimSpace(system) != "" {
				messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: system})
			}
			messages = append(messages, llm.Message{Role: llm.RoleUser, Content: user})
			return llm.Complete(ctx, driver, messages)
		},
		Yolo: func() bool { return yoloOn.Load() },
		SetYolo: func(on bool) {
			yoloOn.Store(on)
			if on {
				evRenderer.Info("yolo on — tool approvals skipped")
			} else {
				evRenderer.Info("yolo off — tools ask before running")
			}
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
			cfg, authTokens := refreshChatSetupState(setup)
			d := setup.MakeDriver(name)
			if d == nil {
				return "", errors.New(bootstrap.DriverUnavailableReason(cfg, authTokens, name))
			}
			setup.ChatModel = name
			setup.Driver = d
			setup.applyChatParams(d)
			if reactRunner != nil {
				reactRunner.SetDriver(setup.Driver)
			}
			persistChatLastModel(setup.Config, name)
			return name, nil
		},
		SetEffort: func(effort string) error {
			effort = strings.TrimSpace(effort)
			ref := bootstrap.ParseModelRef(setup.ChatModel)
			info := modelcatalog.Lookup(ref.Provider, ref.Model)
			if effort != "" {
				supported := false
				if info != nil {
					for _, v := range info.ReasoningEfforts {
						if strings.EqualFold(v, effort) {
							effort = v
							supported = true
							break
						}
					}
				}
				if !supported {
					return fmt.Errorf("model %q does not support reasoning effort %q", setup.ChatModel, effort)
				}
			}
			setup.ReasoningEffort = effort
			setup.reasoningEffortChosen = true
			if setup.Driver != nil {
				if setup != nil {
					setup.applyChatParams(setup.Driver)
				}
			}
			return nil
		},
		CurrentEffort: func() string { return setup.ReasoningEffort },
		ModelEfforts: func(model string) []string {
			ref := bootstrap.ParseModelRef(model)
			if info := modelcatalog.Lookup(ref.Provider, ref.Model); info != nil {
				return append([]string(nil), info.ReasoningEfforts...)
			}
			return nil
		},
		ClearHistory: func() {
			state.Clear()
			if reactRunner != nil {
				reactRunner.ClearHistory()
			}
		},
		Remember: remember,
		CurrentThreadID: func() string {
			return session.DurableThreadID()
		},
		RestoreHistory: func(threadID string) (int, error) {
			outputDir := setup.Config.ResolvedOutputDir()
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
			n, err := session.AdoptReplayItems(items)
			if err != nil {
				return 0, err
			}
			// Restore changes both model context and durable destination. Keeping
			// old sink made UI highlight one thread while writes and delete guards
			// still treated previous thread as active.
			session.SetDurableThreadID(threadID)
			session.SetDurableSink(sessionstore.NewLiveSession(threadID, store, sessionstore.DefaultPersistencePolicy()))
			return n, nil
		},
		ApprovalCh:      evRenderer.ApprovalChan(),
		ResponseCh:      evRenderer.ResponseChan(),
		Skills:          loadedSkills,
		State:           state,
		CopilotClientID: setup.Config.CopilotClientID(),
		ReloadPlugins:   reloadPlugins,
		ListThreads: func() []tui.ThreadSummary {
			outputDir := setup.Config.ResolvedOutputDir()
			if outputDir == "" {
				return nil
			}
			store := sessionstore.NewJSONLThreadStore(filepath.Join(outputDir, "threads"))
			records, err := store.ListThreads(context.Background(), sessionstore.ListOptions{Limit: 500})
			if err != nil {
				return nil
			}
			out := make([]tui.ThreadSummary, 0, len(records))
			for _, r := range records {
				out = append(out, tui.ThreadSummary{
					ThreadID:  r.ThreadID,
					Title:     r.Metadata.Title,
					Preview:   r.Metadata.Preview,
					Model:     r.Metadata.Model,
					CWD:       r.Metadata.CWD,
					UpdatedAt: r.Metadata.UpdatedAt,
					ItemCount: r.ItemCount,
				})
			}
			return out
		},
		DeleteThread: func(threadID string) error {
			outputDir := setup.Config.ResolvedOutputDir()
			if outputDir == "" {
				return errors.New("no session output directory configured")
			}
			if threadID != "" && threadID == session.DurableThreadID() {
				return errors.New("cannot delete the thread you are in")
			}
			store := sessionstore.NewJSONLThreadStore(filepath.Join(outputDir, "threads"))
			return store.DeleteThread(context.Background(), threadID)
		},
		MCPServers: func() []tui.MCPServerStatus {
			byServer := map[string][]string{}
			for _, tool := range mcpManager.Tools() {
				byServer[tool.ServerName] = append(byServer[tool.ServerName], tool.Name)
			}
			names := make([]string, 0, len(setup.Config.MCPServers))
			for name := range setup.Config.MCPServers {
				names = append(names, name)
			}
			sort.Strings(names)
			out := make([]tui.MCPServerStatus, 0, len(names))
			for _, name := range names {
				server := setup.Config.MCPServers[name]
				target := strings.TrimSpace(server.URL)
				if target == "" {
					target = strings.Join(server.Command, " ")
				}
				tools := byServer[name]
				sort.Strings(tools)
				out = append(out, tui.MCPServerStatus{
					Name:    name,
					Type:    inferMCPType(server),
					Target:  target,
					Enabled: server.IsEnabled(),
					Loaded:  len(tools) > 0,
					Tools:   tools,
				})
			}
			return out
		},
		RenameThread: func(threadID, title string) error {
			outputDir := setup.Config.ResolvedOutputDir()
			if outputDir == "" {
				return errors.New("no session output directory configured")
			}
			store := sessionstore.NewJSONLThreadStore(filepath.Join(outputDir, "threads"))
			return store.SetThreadTitle(context.Background(), threadID, title)
		},
		ReadThreadItems: func(threadID string) []protocol.Item {
			outputDir := setup.Config.ResolvedOutputDir()
			if outputDir == "" {
				return nil
			}
			store := sessionstore.NewJSONLThreadStore(filepath.Join(outputDir, "threads"))
			items, err := store.ReadItems(context.Background(), threadID)
			if err != nil {
				return nil
			}
			return items
		},
	}
	if setup.LiveRunner != nil {
		setup.LiveRunner(eventsCh, liveCfg, inputCh, doneCh)
		// The runner returning means this chat is finished — the window is
		// closing, or it is switching to another workspace. Release what holds
		// subprocesses, so a switch does not strand MCP servers and shells.
		close(inputCh)
		if mcpManager != nil {
			_ = mcpManager.Close()
		}
		if execManager != nil {
			execManager.Close()
		}
		lsp.Shared().Close(context.Background())
		return
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
// lspDiagnosticsProvider type-checks the files a turn changed through the
// pooled language servers, so type errors reach the model before it spends a
// turn running the build to find them.
func lspDiagnosticsProvider(workDir string) func(context.Context, []string) string {
	return func(ctx context.Context, changedFiles []string) string {
		resolved := make([]string, 0, len(changedFiles))
		for _, path := range changedFiles {
			if !filepath.IsAbs(path) {
				path = filepath.Join(workDir, path)
			}
			resolved = append(resolved, path)
		}
		out, err := lsp.Shared().Diagnostics(ctx, workDir, resolved)
		if err != nil {
			return ""
		}
		return out
	}
}

func lookupContextWindow(model string) int {
	ref := bootstrap.ParseModelRef(model)
	if info := modelcatalog.Lookup(ref.Provider, ref.Model); info != nil {
		return info.ContextWindow
	}
	// Unlisted model: the catalog trails the providers, and custom providers
	// serve models it never carries. An unknown window makes the runtime pick
	// its most conservative limits — small inline tool results, small prompt
	// budget — so fall back to what the provider says, then to the smallest
	// window the catalog lists for that provider's other models.
	if window := customProviderContextWindow(ref.Provider); window > 0 {
		return window
	}
	return modelcatalog.ProviderMinContextWindow(ref.Provider)
}

func customProviderContextWindow(provider string) int {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return 0
	}
	defs, err := bootstrap.LoadCustomCompatProviders(fsutil.ForgeConfigDir())
	if err != nil {
		return 0
	}
	for _, def := range defs {
		if def.ID == provider {
			return def.ContextWindow
		}
	}
	return 0
}

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
	// plain-http named hosts are treated as LAN; hosted APIs are https
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

type consoleRuntime struct {
	session      *reactruntime.Session
	renderer     *agent.Renderer
	runner       *reactruntime.Runner
	loadedSkills []skills.Skill
	state        *chatstate.State
	remember     func(string) bool
	// execWake receives a signal when a background exec session exits, so an
	// idle console loop can run a turn over the queued completion note
	// instead of waiting for the next user message.
	execWake chan struct{}
}

// buildConsoleRuntime wires the full chat runtime (tools, approval gate,
// plugins, MCP, memory, react runner) shared by the interactive console and
// headless (-p) execution. When approve is nil the console default applies
// (yolo or interactive stdin). The returned cleanup closes preview/MCP/plugin
// resources and must be called by the caller.
func buildConsoleRuntime(setup *ChatSetup, approve tools.ApprovalFunc, out io.Writer, colors bool) (*consoleRuntime, func()) {
	if out == nil {
		out = os.Stdout
	}
	session := reactruntime.NewSession()
	session.SetActiveWorkspaceRoot(setup.WorkDir)
	adoptResumeThread(setup, session)
	if approve == nil {
		if setup.Yolo {
			approve = agent.YoloApproval()
		} else {
			approve = agent.InteractiveApproval(os.Stdin, os.Stdout)
		}
	}
	gate := reactruntime.NewApprovalGate(setup.WorkDir, loadChatApprovalConfig(setup), approve, func(text string) {
		_, _ = fmt.Fprintln(out, text)
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
	renderer := agent.NewRenderer(out, 80, colors)
	forcePromptApprove := approve
	// Late-bound so the exec-status callback can feed background command
	// completions back to the runner (created further below).
	var reactRunner *reactruntime.Runner
	execWake := make(chan struct{}, 1)
	previewRuntime, mcpManager, execManager := registerTools(reg, setup.WorkDir, setup.Config, session, approve, renderer.Info, func(status tools.ExecSessionStatus) {
		payload, err := json.Marshal(status)
		if err != nil {
			renderer.Info(fmt.Sprintf("command session %d changed state", status.SessionID))
			return
		}
		renderer.ToolResult("command_status", string(payload), "", false)
		if status.Status == "exited" && reactRunner != nil {
			reactRunner.QueuePendingInput(backgroundExitNote(status))
			select {
			case execWake <- struct{}{}:
			default:
			}
		}
	}, forcePromptApprove)
	pluginManager := startChatPluginManager(setup.Config, setup.WorkDir, renderer.Info)
	if pluginManager != nil {
		pluginManager.RegisterTools(reg, approve)
	}
	lsp.Shared().SetServers(lsp.ServersFromConfig(setup.Config.LSP))
	cleanup := func() {
		if previewRuntime != nil {
			_ = previewRuntime.Close()
		}
		if mcpManager != nil {
			_ = mcpManager.Close()
		}
		if execManager != nil {
			execManager.Close()
		}
		if pluginManager != nil {
			_ = pluginManager.Close()
		}
		lsp.Shared().Close(context.Background())
	}
	baseReg := reg.Filter(nil)
	loadedSkills := skills.Load(setup.WorkDir)
	state := chatstate.New()
	memPipeline := memory.Pipeline{MaxRecords: 12}
	memState := memory.LoadState(setup.WorkDir)
	if memState.Summary != "" {
		session.SetMemorySummary(memState.Summary)
	}
	var memMu sync.Mutex
	remember := func(text string) bool {
		memMu.Lock()
		defer memMu.Unlock()
		next, ok := memPipeline.Remember(memState, text)
		if !ok {
			return false
		}
		memState = next
		session.SetMemorySummary(next.Summary)
		_ = memory.SaveState(setup.WorkDir, next)
		return true
	}
	lean := leanToolExposure(setup)
	reactRunner = reactruntime.NewRunner(reactruntime.Config{
		Driver:           setup.Driver,
		Tools:            reg,
		Renderer:         renderer,
		ShowReasoning:    setup.Config.Chat.ReasoningVisible(),
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
			memMu.Lock()
			defer memMu.Unlock()
			if next, ok := memPipeline.Process(memState, snapshot); ok {
				memState = next
				session.SetMemorySummary(next.Summary)
				_ = memory.SaveState(setup.WorkDir, next)
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
		ContextWindowTokens:      func() int { return lookupContextWindow(setup.ChatModel) },
		DiagnosticsProvider:      lspDiagnosticsProvider(setup.WorkDir),
		Interactive:              true,
		ToolThrashCircuitBreaker: setup.Config.Resilience.ToolThrashCircuitBreaker,
		OutputStore:              configuredOutputStore(setup.Config),
	})
	registerReactDelegationTools(reg, setup, baseReg, approve, nil, pluginManager, session)

	return &consoleRuntime{
		session:      session,
		renderer:     renderer,
		runner:       reactRunner,
		loadedSkills: loadedSkills,
		state:        state,
		remember:     remember,
		execWake:     execWake,
	}, cleanup
}

func RunChatConsole(setup *ChatSetup) {
	// The initial driver was never given generation params: they were only
	// applied on a model switch or an /effort change, so a configured effort
	// did nothing until one of those happened, and a model that only reasons
	// when asked never reasoned at all.
	if setup != nil {
		setup.applyChatParams(setup.Driver)
	}
	rt, cleanup := buildConsoleRuntime(setup, nil, os.Stdout, true)
	defer cleanup()
	renderer := rt.renderer
	reactRunner := rt.runner
	loadedSkills := rt.loadedSkills
	state := rt.state
	remember := rt.remember

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

	// Stdin lines arrive over a channel so the loop can also wake on
	// background exec completions while idle.
	lines := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	for {
		select {
		case <-pendingShutdown:
			fmt.Println("\ninterrupted")
			return
		default:
		}
		renderer.Prompt()
		var input string
		select {
		case <-pendingShutdown:
			fmt.Println("\ninterrupted")
			return
		case <-rt.execWake:
			// A background command finished while idle: run a turn over the
			// queued completion note so the model reacts without user input.
			queued := reactRunner.DiscardPendingInput()
			if len(queued) == 0 {
				continue
			}
			turnCtx, tc := context.WithCancel(ctx)
			turnCancel.Store(tc)
			note := strings.Join(queued, "\n\n")
			if err := runChatTurn(turnCtx, reactRunner, chatstate.ChatUserInput{IsInput: true, Text: note}); err != nil {
				renderer.Error(err.Error())
			}
			continue
		case line, ok := <-lines:
			if !ok {
				fmt.Println()
				return
			}
			input = strings.TrimSpace(line)
		}
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" || input == "/exit" || input == "/quit" {
			break
		}
		if strings.HasPrefix(input, "/") {
			if strings.HasPrefix(input, "/remember") {
				text := strings.TrimSpace(strings.TrimPrefix(input, "/remember"))
				switch {
				case text == "":
					renderer.Info("usage: /remember <text>")
				case remember(text):
					renderer.Info("remembered (pinned)")
				default:
					renderer.Info("nothing to remember")
				}
				continue
			}
			// Check plugin commands first
			pluginHandled := false
			if pluginCommands := plugin.Global().GetAllCommands(); len(pluginCommands) > 0 {
				for _, cmd := range pluginCommands {
					cmdName := strings.TrimPrefix(cmd.Name, "/")
					prefix := "/" + cmdName
					if input == prefix || strings.HasPrefix(input, prefix+" ") {
						args := ""
						if len(input) > len(prefix) {
							args = strings.TrimSpace(input[len(prefix):])
						}
						result, err := cmd.Handler(ctx, args)
						if err != nil {
							renderer.Error(fmt.Sprintf("plugin command failed: %v", err))
						} else if result != "" {
							renderer.Info(result)
						}
						pluginHandled = true
						break
					}
				}
			}
			if pluginHandled {
				continue
			}
			handled := handleChatSlashCommand(input, renderer, loadedSkills, state, reactRunner, setup)
			if handled {
				continue
			}
		}
		ui := chatstate.ChatUserInput{IsInput: true, Text: input}
		turnCtx, tc := context.WithCancel(ctx)
		turnCancel.Store(tc)
		err := runChatTurn(turnCtx, reactRunner, ui)
		if err != nil {
			renderer.Error(err.Error())
		}
	}
	fmt.Println()
}

// RunChatHeadless runs a single prompt non-interactively and returns a process
// exit code. Progress, tool activity, and errors go to stderr; only the final
// assistant response is written to stdout so it can be piped. Approvals are not
// interactive: without --yolo every approval-gated action is denied.
func RunChatHeadless(setup *ChatSetup, prompt string) int {
	// The initial driver was never given generation params: they were only
	// applied on a model switch or an /effort change, so a configured effort
	// did nothing until one of those happened, and a model that only reasons
	// when asked never reasoned at all.
	if setup != nil {
		setup.applyChatParams(setup.Driver)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "error: empty prompt")
		return 2
	}
	var approve tools.ApprovalFunc
	if !setup.Yolo {
		approve = func(tools.Action) (bool, error) { return false, nil }
	}
	rt, cleanup := buildConsoleRuntime(setup, approve, os.Stderr, false)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()

	turnErr := runChatTurn(ctx, rt.runner, chatstate.ChatUserInput{IsInput: true, Text: prompt})
	// Print whatever the run produced even when it ended badly. A failure
	// several minutes and many tool calls in still holds the work done so far,
	// and exiting silently made that unrecoverable for anything piping forge.
	if resp := strings.TrimSpace(rt.runner.LastResponse()); resp != "" {
		_, _ = fmt.Fprintln(os.Stdout, resp)
	}
	if turnErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", turnErr)
		return 1
	}
	return 0
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
			switch {
			case agent.Model != "":
				model = agent.Model
			case agent.Role != "" && setup.Config != nil:
				if roleModel := setup.Config.RoleModel(agent.Role); roleModel != "" {
					model = roleModel
				}
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
		childTools := childRegistryForRole(childBaseReg)
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
			ContextWindowTokens:      func() int { return lookupContextWindow(model) },
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
		case "write_file", "edit_file", "apply_patch", "ast_edit", "artifact_write", "run_command", "exec_session_start", "exec_session_write", "command_write_stdin", "git_commit", "git_push":
			continue
		default:
			out = append(out, name)
		}
	}
	return out
}

func childRegistryForRole(base *tools.Registry) *tools.Registry {
	if base == nil {
		return tools.NewRegistry()
	}
	return base.Filter(childReadOnlyToolNames())
}

func childReadOnlyToolNames() []string {
	return []string{
		"read_file", "read_output", "artifact_read", "list_dir", "search", "code_search", "ast_grep", "glob", "view_image",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols",
		"git_status", "git_diff", "git_log", "git_branch_state", "git_merge_status",
		"tool_help", "think",
	}
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
	if tools.AstGrepAvailable() {
		childReg.Register(tools.NewAstGrep(childWorkDir, secretPolicy))
	}
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
	childReg.Register(tools.NewGitShow(childWorkDir))
	childReg.Register(tools.NewGitBlame(childWorkDir))
	childReg.Register(tools.NewGHPullRequest(childWorkDir))
	childReg.Register(tools.NewReviewDiff(childWorkDir))
	childReg.Register(tools.NewReportFindings())
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
func (r agentProgressRenderTarget) StatsWithContext(duration time.Duration, usage llm.Usage, contextUsed, contextLimit int) {
	if target, ok := r.target.(agent.ContextStatsTarget); ok {
		target.StatsWithContext(duration, usage, contextUsed, contextLimit)
		return
	}
	r.target.Stats(duration, usage)
}
func (r agentProgressRenderTarget) Error(msg string) { r.target.Error(msg) }
func (r agentProgressRenderTarget) Info(msg string)  { r.target.Info(msg) }

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
	GuardianEvent *reactruntime.GuardianEvent
}

func newChatHookRegistry() *hooks.Registry {
	registry := hooks.NewRegistry()
	registry.Register(hooks.PointPromptContext, "guardian_warning", guardianWarningPromptHook)
	return registry
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
	Checkpoints() []reactruntime.CheckpointRef
	RestoreCheckpoint(context.Context, string) error
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
			cfg, authTokens := refreshChatSetupState(setup)
			renderer.Error(bootstrap.DriverUnavailableReason(cfg, authTokens, picked))
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
			cfg, authTokens := refreshChatSetupState(setup)
			renderer.Error(bootstrap.DriverUnavailableReason(cfg, authTokens, newModel))
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
	case input == "/rewind" || strings.HasPrefix(input, "/rewind "):
		if session == nil {
			renderer.Error("rewind unavailable")
			return true
		}
		checkpoints := session.Checkpoints()
		if len(checkpoints) == 0 {
			renderer.Info("no workspace checkpoints yet")
			return true
		}
		arg := strings.TrimSpace(strings.TrimPrefix(input, "/rewind"))
		if arg == "list" {
			fmt.Println()
			for _, cp := range checkpoints {
				fmt.Printf("  %s (%s)\n", cp.TurnID, cp.ID)
			}
			fmt.Println()
			return true
		}
		target := checkpoints[len(checkpoints)-1]
		if arg != "" {
			found := false
			for _, cp := range checkpoints {
				if cp.TurnID == arg || cp.ID == arg {
					target, found = cp, true
					break
				}
			}
			if !found {
				renderer.Error(fmt.Sprintf("unknown checkpoint %q — try /rewind list", arg))
				return true
			}
		}
		if err := session.RestoreCheckpoint(context.Background(), target.ID); err != nil {
			renderer.Error(fmt.Sprintf("rewind failed: %v", err))
			return true
		}
		renderer.Info(fmt.Sprintf("workspace reverted to checkpoint %s (%s)", target.TurnID, target.ID))
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
		// Check plugin commands first
		cmd := strings.TrimPrefix(input, "/")
		name, args, hasArgs := strings.Cut(cmd, " ")
		args = strings.TrimSpace(args)

		// Look for plugin commands
		if p := plugin.GetPlugin(name); p != nil {
			for _, c := range p.Commands {
				if c.Name == name {
					if hasArgs {
						output, err := c.Handler(context.Background(), args)
						if err != nil {
							renderer.Error(err.Error())
						} else {
							renderer.Info(output)
						}
					} else {
						renderer.Error(fmt.Sprintf("usage: /%s <args>", name))
					}
					return true
				}
			}
		}

		// Skill/command activation: /<name> or /<name> <args>. A skill body
		// with $ARGUMENTS has the remainder substituted in (falling back to
		// appending it), so a skill file doubles as a parameterized command.
		s, ok := skills.Get(loadedSkills, name)
		if !ok {
			// Skill names may contain spaces; retry against the whole token.
			s, ok = skills.Get(loadedSkills, cmd)
			hasArgs, args = false, ""
		}
		if !ok {
			renderer.Error(fmt.Sprintf("unknown command: %s (try /help)", input))
			return true
		}
		if !hasArgs && state != nil && state.SkillActivated(s.Name) {
			renderer.Info(fmt.Sprintf("skill already active: %s", s.Name))
			return true
		}
		body := s.Body
		switch {
		case strings.Contains(body, "$ARGUMENTS"):
			body = strings.ReplaceAll(body, "$ARGUMENTS", args)
		case args != "":
			body = body + "\n\n" + args
		}
		if state != nil {
			state.ActivateSkill(s.Name)
		}
		if session != nil {
			session.AppendSkillContext(s.Name, body)
		}
		renderer.Info(fmt.Sprintf("skill activated: %s", s.Name))
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
	fmt.Println("    /rewind         revert workspace to the latest checkpoint")
	fmt.Println("    /rewind list    list workspace checkpoints")
	fmt.Println("    /rewind <turn>  revert to a specific checkpoint")
	fmt.Println("    /trace          open debug trace overlay (requires forge -d)")
	fmt.Println("    /skills         list available skills and how to activate them")
	fmt.Println("    /<skill> [args] activate a loaded skill; args fill $ARGUMENTS")
	fmt.Println("                     example: /skills, then /tdd")
	fmt.Println("    /help           show this help")
	fmt.Println("    /exit           exit chat")
	fmt.Println()
	fmt.Println("  Debug:")
	fmt.Println("    forge -d        open the advanced debug view and write a fresh debug log")
	fmt.Println()
}
