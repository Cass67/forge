package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/chatgptauth"
	"forge/internal/chatstate"
	"forge/internal/claudeauth"
	"forge/internal/codexusage"
	"forge/internal/copilot"
	"forge/internal/llm"
	"forge/internal/plugin"
	"forge/internal/skills"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"
)

type chatTickMsg time.Time
type chatApprovalMsg tools.Action

// pluginCommandResultMsg carries the output of a plugin slash command
// back into the Bubble Tea update loop.
type pluginCommandResultMsg struct {
	content string
	err     error
}

type providerAuthStartedMsg struct {
	providerID string
	verifyURL  string
	userCode   string
	flow       any
}
type providerAuthSucceededMsg struct {
	providerID    string
	session       *chatgptauth.Session
	claudeSession *claudeauth.Session
	token         string
}
type providerAuthFailedMsg struct {
	providerID string
	err        error
}
type providerAuthURLOpenedMsg struct {
	target string
}
type providerAuthURLOpenFailedMsg struct {
	target string
	err    error
}
type statsCopilotQuotaMsg struct {
	model string
	quota *copilot.UserQuota
	err   error
}
type statsCodexUsageMsg struct {
	model    string
	snapshot *codexusage.Snapshot
	err      error
}
type chatSlashCompletionState struct {
	baseInput string
	matches   []string
	index     int
}

func wrapProviderAuthValue(value string, width int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return wrap.String(value, max(1, width))
}

func providerAuthHyperlink(label, target string) string {
	label = strings.TrimSpace(label)
	target = strings.TrimSpace(target)
	if label == "" {
		label = target
	}
	if target == "" {
		return label
	}
	return "\x1b]8;;" + target + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

var (
	ansiCSIPattern = regexp.MustCompile(`\x1b\[[0-9:;<=>?]*[ -/]*[@-~]`)
	ansiOSCPattern = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

	startChatGPTDeviceAuth = func(ctx context.Context) (*chatgptauth.DeviceFlow, error) {
		return chatgptauth.StartDeviceAuth(ctx)
	}
	waitChatGPTDeviceAuth = func(ctx context.Context, flow *chatgptauth.DeviceFlow) (chatgptauth.Session, error) {
		return flow.Wait(ctx)
	}
	startClaudeAuth = func() (*claudeauth.Flow, error) {
		return claudeauth.StartAuth()
	}
	exchangeClaudeAuth = func(ctx context.Context, flow *claudeauth.Flow, pasted string) (claudeauth.Session, error) {
		return claudeauth.Exchange(ctx, nil, flow, pasted)
	}
	startCopilotDeviceAuth = func(ctx context.Context, clientID string) (*copilot.DeviceCode, error) {
		return copilot.RequestDeviceCode(ctx, clientID)
	}
	waitCopilotDeviceAuth = func(ctx context.Context, clientID string, dc *copilot.DeviceCode) (string, error) {
		return copilot.PollForToken(ctx, clientID, dc)
	}
	openProviderAuthURL = func(target string) tea.Cmd {
		return func() tea.Msg {
			if err := openExternalURL(target); err != nil {
				return providerAuthURLOpenFailedMsg{target: target, err: err}
			}
			return providerAuthURLOpenedMsg{target: target}
		}
	}
)

func openExternalURL(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("missing URL")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

// toolsSection represents a contiguous block in the tools pane, either from
// the main agent or a sub-agent. Sub-agent sections can be collapsed once
// the sub-agent completes, showing only a one-line summary.
type toolsSection struct {
	role      string // "" for main agent tools
	buf       string // full detail
	summary   string // collapsed summary (set on completion)
	collapsed bool   // true after sub-agent completes
	turnCount int
	toolCount int

	tokenRun          string
	transcriptVisible bool
}

// ChatModel is the Bubble Tea model for the interactive chat screen.
type chatPaneFocus int

const (
	focusChat chatPaneFocus = iota
	focusTools
)

type chatFollowMode int

const (
	followBottom chatFollowMode = iota
	followTurnStart
	followManual
)

const chatPaneBorderHeight = 0
const chatHeaderGapHeight = 1
const chatComposerGapHeight = 1
const chatStatusHeight = 0
const chatDebugDockHeight = 8
const transcriptVirtualizationThreshold = 200
const transcriptVirtualizationOverscan = 30

type subAgentSummary struct {
	role              string
	turns             int
	tools             int
	transcriptVisible bool
}

type chatAgentTaskActivity struct {
	ToolName string    `json:"tool_name"`
	Summary  string    `json:"summary,omitempty"`
	At       time.Time `json:"at"`
}

type chatAgentTaskState struct {
	ID             string                  `json:"id"`
	Role           string                  `json:"role"`
	Status         string                  `json:"status"`
	CreatedAt      time.Time               `json:"created_at"`
	StartedAt      time.Time               `json:"started_at,omitempty"`
	CompletedAt    time.Time               `json:"completed_at,omitempty"`
	LastActivityAt time.Time               `json:"last_activity_at,omitempty"`
	Result         string                  `json:"result,omitempty"`
	Error          string                  `json:"error,omitempty"`
	LastToolName   string                  `json:"last_tool_name,omitempty"`
	RecentActivity []chatAgentTaskActivity `json:"recent_activity,omitempty"`
}

// toolCallEntry records a single tool invocation for display.
type toolCallEntry struct {
	ToolName string `json:"tool_name"`
	Target   string `json:"target,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Status   string `json:"status,omitempty"` // "running", "done", "error"
	AgentID  string `json:"agent_id,omitempty"`
}

// fileChangesTracker tracks files modified/added/deleted during a session.
type fileChangesTracker struct {
	Modified []string `json:"modified,omitempty"`
	Added    []string `json:"added,omitempty"`
	Deleted  []string `json:"deleted,omitempty"`
}

func (f *fileChangesTracker) markModified(path string) {
	if path == "" {
		return
	}
	for _, p := range f.Modified {
		if p == path {
			return
		}
	}
	f.Modified = append(f.Modified, path)
}

func (f *fileChangesTracker) Total() int {
	return len(f.Modified) + len(f.Added) + len(f.Deleted)
}

func (f *fileChangesTracker) Paths() []string {
	var all []string
	all = append(all, f.Modified...)
	all = append(all, f.Added...)
	all = append(all, f.Deleted...)
	return all
}

type ChatModel struct {
	config  ChatLiveConfig
	model   string
	workDir string
	copyFn  func(string) error

	messages []ChatMessage
	records  []TranscriptRecord

	inputBuf    string
	inputPos    int
	attachments []chatstate.ChatAttachment

	width  int
	height int

	chatViewport    viewport.Model
	chatContent     string
	chatVisible     string
	paneFocus       chatPaneFocus
	discardMouseCSI bool
	toolsScroll     int
	followMode      chatFollowMode
	debugEnabled    bool
	traceVisible    bool

	toolsSections          []toolsSection
	toolsVisible           bool
	toolsWasShowing        bool
	agentPanelHiddenByUser bool
	agentViewVisible       bool
	agentViewIndex         int
	agentTasks             []chatAgentTaskState
	toolPanelsVisible      bool
	recentToolCalls        []toolCallEntry
	fileChanges            fileChangesTracker
	lastToolResult         string
	lastCodeBlock          string
	lastToolSummary        map[string]string

	busy                   bool
	viewportDirty          bool
	spinnerFrame           int
	status                 string
	activeSubAgent         string
	lastEscapeTime         time.Time
	flash                  string
	restoreNote            string // set by applySnapshot when durable history replay ran
	lastProgressCheckpoint string
	lastProgressAt         time.Time
	statsDuration          time.Duration
	statsUsage             llm.Usage
	liveStatsStartedAt     time.Time
	liveStatsOutputChars   int
	sessionUsage           llm.Usage
	statusData             chatStatusData
	recentActivityRole     string
	recentActivityLines    []string
	recentActivityIndex    int
	liveProgress           LiveProgressState
	turnAnchorMessageIndex int
	pendingSubAgentSummary *subAgentSummary
	skills                 []skills.Skill
	state                  *chatstate.State
	themeID                string
	pendingQueuedInput     []string

	helpVisible bool
	helpTab     int
	helpScroll  int

	statsVisible        bool
	statsCopilotLoading bool
	statsCopilotErr     string
	statsCodexLoading   bool
	statsCodexErr       string

	searchVisible bool
	searchQuery   string
	searchPos     int
	searchPane    chatPaneFocus
	searchMatches []int
	searchCurrent int

	modelsVisible  bool
	modelsCursor   int
	modelsList     []string
	modelsFiltered []string
	modelsQuery    string
	modelsQueryPos int

	providersVisible     bool
	providersCursor      int
	providersList        []ProviderOption
	providerPromptingKey bool
	providerKeyInput     string
	providerKeyPos       int
	providerPromptLabel  string
	providerPromptMasked bool
	providerStatus       string
	providerAuthURL      string
	providerAuthCode     string
	providerAuthWaiting  bool
	providerAuthProvider string
	providerAuthCancel   context.CancelFunc
	providerAuthFlow     any

	sessionsVisible  bool
	sessionsCursor   int
	sessionsList     []chatSessionEntry
	sessionRenaming  bool
	sessionRenameBuf string
	sessionRenamePos int

	contextFiles    []string
	filesVisible    bool
	filesBrowser    bool
	filesViewing    bool
	filesCursor     int
	filesList       []string
	filesFiltered   []string
	filesQuery      string
	filesPos        int
	filesViewPath   string
	filesViewText   string
	filesViewScroll int

	pendingApproval *tools.Action
	inputCh         chan<- string
	responseCh      chan<- bool
	slashComplete   chatSlashCompletionState
	nextRecordSeq   int
	renderCache     *transcriptRenderCache
	lastRenderStats transcriptRenderStats
}

type transcriptRenderStats struct {
	Rendered int
	Hits     int
	Misses   int
	Lines    int
}

func NewChatModel(cfg ChatLiveConfig) ChatModel {
	vp := viewport.New(80, 20)
	vp.SetContent("")

	state := cfg.State
	if state == nil {
		state = chatstate.New()
	}

	m := ChatModel{
		config:                 cfg,
		model:                  cfg.Model,
		workDir:                cfg.WorkDir,
		copyFn:                 copyToClipboard,
		themeID:                "default",
		chatViewport:           vp,
		followMode:             followBottom,
		debugEnabled:           cfg.DebugEnabled,
		status:                 "ready",
		skills:                 cfg.Skills,
		state:                  state,
		toolsVisible:           false,
		paneFocus:              focusChat,
		recentActivityIndex:    -1,
		turnAnchorMessageIndex: -1,
		lastToolSummary:        make(map[string]string),
		modelsList:             uniqueStringsPreserveOrder(cfg.AvailableModels),
		modelsFiltered:         uniqueStringsPreserveOrder(cfg.AvailableModels),
		providersList:          append([]ProviderOption(nil), cfg.Providers...),
		contextFiles:           append([]string(nil), cfg.ContextFiles...),
		renderCache:            newTranscriptRenderCache(),
	}
	m.modelsList = m.uniqueModelOptions(m.modelsList)
	m.modelsFiltered = append([]string(nil), m.modelsList...)
	m.syncStatusData()
	return m
}

func (m ChatModel) Init() tea.Cmd {
	return tea.Batch(
		tea.ClearScreen,
		m.chatViewport.Init(),
		tickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return chatTickMsg(t)
	})
}

func chatSpinnerGlyph(frame int) string {
	frames := []string{"-", "\\", "|", "/"}
	if len(frames) == 0 {
		return "-"
	}
	return frames[frame%len(frames)]
}

func (m *ChatModel) AddMessage(msg ChatMessage) {
	m.messages = append(m.messages, msg)
	m.appendTranscriptRecordFromMessage(msg)
	m.refreshViewport()
}

func (m *ChatModel) upsertPlanMessage(content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	msg := ChatMessage{Kind: MsgPlan, Header: "Plan", Content: content}
	for i := range m.messages {
		if m.messages[i].Kind != MsgPlan {
			continue
		}
		m.messages[i] = msg
		m.rebuildTranscriptStateFromMessages()
		m.refreshViewport()
		return
	}
	m.AddMessage(msg)
}

func (m *ChatModel) upsertTaskContextMessage(content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	msg := ChatMessage{Kind: MsgForge, Header: "Task", Content: content}
	for i := range m.messages {
		if m.messages[i].Kind == MsgForge && strings.TrimSpace(m.messages[i].Header) == "Task" {
			m.messages[i] = msg
			m.rebuildTranscriptStateFromMessages()
			m.refreshViewport()
			return
		}
	}
	m.AddMessage(msg)
}

func (m *ChatModel) AddWorkingMessage(content string) {
	m.recordWorkingMessage(strings.TrimSpace(content))
}

func (m *ChatModel) UpdateRecentActivity(role, content string) {
	role = strings.TrimSpace(role)
	content = formatWorkingLine(role, content)
	if content == "" {
		return
	}
	m.recordWorkingMessage(content)
}

func (m *ChatModel) hasLiveWorkingMessage() bool {
	if m.recentActivityIndex < 0 || m.recentActivityIndex >= len(m.messages) {
		return false
	}
	msg := m.messages[m.recentActivityIndex]
	return msg.Kind == MsgWorking
}

func (m *ChatModel) recordWorkingMessage(content string) {
	content = normalizeProgressMessage(content)
	if content == "" {
		return
	}
	if m.hasLiveWorkingMessage() {
		idx := m.recentActivityIndex
		current := strings.TrimSpace(m.messages[idx].Content)
		next := combineProgressNarrative(current, content)
		if next == "" {
			return
		}
		if current == next {
			return
		}
		m.messages[idx].Content = next
		m.liveProgress = m.liveProgress.Apply(ProgressUpdate{
			ReplaceKey: "active",
			Message:    next,
		})
		m.refreshViewport()
		return
	}
	m.liveProgress = m.liveProgress.Apply(ProgressUpdate{
		ReplaceKey: "active",
		Message:    content,
	})
	m.messages = append(m.messages, ChatMessage{
		Kind:    MsgWorking,
		Content: content,
	})
	m.recentActivityIndex = len(m.messages) - 1
	m.refreshViewport()
}

func (m *ChatModel) clearWorkingMessage() {
	if !m.hasLiveWorkingMessage() {
		m.liveProgress = m.liveProgress.Reset()
		m.resetRecentActivity()
		return
	}
	idx := m.recentActivityIndex
	m.messages = append(m.messages[:idx], m.messages[idx+1:]...)
	m.liveProgress = m.liveProgress.Reset()
	m.resetRecentActivity()
	m.refreshViewport()
}

func (m *ChatModel) archiveWorkingMessage() {
	m.clearWorkingMessage()
}

func (m *ChatModel) resetRecentActivity() {
	m.recentActivityRole = ""
	m.recentActivityLines = nil
	m.recentActivityIndex = -1
	m.pendingSubAgentSummary = nil
}

func compactStatusText(content string) string {
	content = strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	if content == "" {
		return ""
	}
	return truncate(content, 200)
}

func isRecoverableToolFeedback(ev llm.Event) bool {
	if ev.Kind != llm.EventToolResult || !ev.IsError || ev.Agent == "" {
		return false
	}
	message := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ev.Text), "error: "))
	if ev.Agent == "ask_user_question" {
		return strings.Contains(message, "options") || strings.Contains(message, "question")
	}
	// Runtime-internal retry feedback: the loop already fed these back to the
	// model as tool results; rendering them as top-level errors reads as a
	// failed turn when the turn is still recovering.
	if strings.Contains(message, "malformed tool call arguments") ||
		strings.Contains(message, "contains the literal marker") {
		return true
	}
	if !strings.HasPrefix(message, ev.Agent+".") {
		return false
	}
	return strings.Contains(message, " is required") ||
		strings.Contains(message, " must be ") ||
		strings.Contains(message, " is not allowed")
}

func sanitizeAssistantTokenForDisplay(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	// Defense-in-depth: never render raw tool-call markup in the user-facing
	// transcript pane.
	if containsRawToolCallMarkup(text) {
		return ""
	}
	return text
}

func containsRawToolCallMarkup(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, "<tool_call") ||
		strings.Contains(lower, "</tool_call") ||
		strings.Contains(lower, "<function_calls") ||
		strings.Contains(lower, "</function_calls") ||
		strings.Contains(lower, "<tool_calls") ||
		strings.Contains(lower, "</tool_calls")
}

func plainCopyText(text string) string {
	if text == "" {
		return ""
	}
	plain := ansiOSCPattern.ReplaceAllString(text, "")
	plain = ansiCSIPattern.ReplaceAllString(plain, "")
	plain = strings.ReplaceAll(plain, "\r\n", "\n")
	plain = strings.ReplaceAll(plain, "\r", "\n")
	return plain
}

func displayAgentLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" || label == "agent" || label == "forge" {
		return "Forge"
	}
	if len(label) > 0 && label[0] >= 'a' && label[0] <= 'z' {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	return label
}

func hasAgentHeaderForLabel(msg ChatMessage, label string) bool {
	label = displayAgentLabel(label)
	return strings.TrimSpace(msg.Header) == label || strings.HasPrefix(msg.Header, label+" • ")
}

func formatWorkingLine(role, content string) string {
	content = strings.TrimSpace(content)
	if role != "" {
		content = strings.TrimSpace(strings.TrimPrefix(content, role+": "))
	}
	if content == "" {
		return ""
	}
	return content
}

func (m *ChatModel) AppendToLastAgent(text string) {
	m.AppendToLastAgentLabeled(text, "Agent")
}

func (m *ChatModel) AppendToLastAgentLabeled(text, label string) {
	if m.hasLiveWorkingMessage() {
		m.archiveWorkingMessage()
	}
	startedNew := false
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].Kind != MsgAgent || !hasAgentHeaderForLabel(m.messages[len(m.messages)-1], label) {
		stamp := time.Now().Format("15:04:05")
		m.messages = append(m.messages, ChatMessage{Kind: MsgAgent, Header: displayAgentLabel(label) + " • " + stamp})
		startedNew = true
	}
	m.messages[len(m.messages)-1].Content += text
	m.syncAssistantTranscriptRecord(label, m.messages[len(m.messages)-1].Header, m.messages[len(m.messages)-1].Content, startedNew)
	m.lastCodeBlock = latestFencedCodeBlock(m.messages[len(m.messages)-1].Content)
	m.viewportDirty = true
}

func (m ChatModel) hasLiveAssistantMessage() bool {
	return len(m.messages) > 0 && m.messages[len(m.messages)-1].Kind == MsgAgent
}

func (m *ChatModel) appendTranscriptRecord(record TranscriptRecord) {
	if len(record.Segments) == 0 {
		return
	}
	if strings.TrimSpace(record.ID) == "" {
		m.nextRecordSeq++
		record.ID = formatTranscriptRecordID(m.nextRecordSeq)
	}
	m.records = append(m.records, record)
}

func (m *ChatModel) appendTranscriptRecordFromMessage(msg ChatMessage) {
	record, ok := transcriptRecordFromMessage(msg, "", 0)
	if !ok {
		return
	}
	m.appendTranscriptRecord(record)
}

func (m *ChatModel) resetTranscriptState() {
	m.records = nil
	m.liveProgress = LiveProgressState{}
	m.nextRecordSeq = 0
}

func (m *ChatModel) rebuildTranscriptStateFromMessages() {
	m.resetTranscriptState()
	for _, msg := range m.messages {
		m.appendTranscriptRecordFromMessage(msg)
	}
}

func (m *ChatModel) syncAssistantTranscriptRecord(label, header, content string, startedNew bool) {
	record := TranscriptRecord{
		Kind:     RecordAssistant,
		Label:    displayAgentLabel(label),
		Segments: segmentsFromContent(content),
		Final:    false,
	}
	if header != "" {
		record.Label = strings.TrimSpace(header)
	}
	if len(record.Segments) == 0 {
		return
	}
	if startedNew || len(m.records) == 0 || m.records[len(m.records)-1].Kind != RecordAssistant {
		m.appendTranscriptRecord(record)
		return
	}
	last := &m.records[len(m.records)-1]
	last.Label = record.Label
	last.Segments = record.Segments
	last.Final = false
}

func (m *ChatModel) markLastAssistantRecordFinal() {
	for i := len(m.records) - 1; i >= 0; i-- {
		if m.records[i].Kind == RecordAssistant {
			m.records[i].Final = true
			return
		}
	}
}

func (m *ChatModel) discardPendingAssistantMessage() {
	hasPendingAssistant := false
	for i := len(m.records) - 1; i >= 0; i-- {
		if m.records[i].Kind != RecordAssistant {
			continue
		}
		if m.records[i].Final {
			return
		}
		hasPendingAssistant = true
		break
	}
	if !hasPendingAssistant {
		return
	}
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Kind != MsgAgent {
			continue
		}
		m.messages = append(m.messages[:i], m.messages[i+1:]...)
		if m.recentActivityIndex > i {
			m.recentActivityIndex--
		}
		m.rebuildTranscriptStateFromMessages()
		m.lastCodeBlock = latestAgentCodeBlock(m.messages)
		m.refreshViewport()
		return
	}
}

func latestAgentCodeBlock(messages []ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Kind != MsgAgent {
			continue
		}
		if block := latestFencedCodeBlock(messages[i].Content); block != "" {
			return block
		}
	}
	return ""
}

func (m *ChatModel) finalizeLiveProgressRecord() {
	record, ok := m.liveProgress.Finalize()
	if !ok {
		m.clearWorkingMessage()
		return
	}
	m.clearWorkingMessage()
	m.appendTranscriptRecord(record)
}

func (m *ChatModel) anchorLatestTurnToBottom() {
	if len(m.messages) == 0 {
		m.turnAnchorMessageIndex = -1
		m.followMode = followBottom
		return
	}
	m.turnAnchorMessageIndex = len(m.messages) - 1
	m.followMode = followBottom
}

func (m *ChatModel) markManualScroll() {
	m.followMode = followManual
}

func (m *ChatModel) applyViewportFollow(totalLines int) {
	maxScroll := max(0, totalLines-max(1, m.chatViewport.Height))
	if m.followMode == followManual {
		m.chatViewport.YOffset = clamp(m.chatViewport.YOffset, 0, maxScroll)
		return
	}
	if m.followMode == followTurnStart {
		m.chatViewport.GotoTop()
		return
	}
	m.chatViewport.GotoBottom()
}

type delegateTranscriptState struct {
	role              string
	transcriptVisible bool
}

func (m ChatModel) delegateResultState() delegateTranscriptState {
	if m.pendingSubAgentSummary != nil && strings.TrimSpace(m.pendingSubAgentSummary.role) != "" {
		return delegateTranscriptState{
			role:              strings.TrimSpace(m.pendingSubAgentSummary.role),
			transcriptVisible: m.pendingSubAgentSummary.transcriptVisible,
		}
	}
	if role := strings.TrimSpace(m.activeSubAgent); role != "" {
		for i := len(m.toolsSections) - 1; i >= 0; i-- {
			if strings.TrimSpace(m.toolsSections[i].role) == role {
				return delegateTranscriptState{
					role:              role,
					transcriptVisible: m.toolsSections[i].transcriptVisible,
				}
			}
		}
		return delegateTranscriptState{role: role}
	}
	for i := len(m.toolsSections) - 1; i >= 0; i-- {
		if role := strings.TrimSpace(m.toolsSections[i].role); role != "" {
			return delegateTranscriptState{
				role:              role,
				transcriptVisible: m.toolsSections[i].transcriptVisible,
			}
		}
	}
	return delegateTranscriptState{}
}

func looksLikeStructuredTranscript(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return true
	}
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.SplitN(trimmed, "\n", 2)
		if len(lines) == 2 {
			body := strings.TrimSpace(strings.TrimSuffix(lines[1], "```"))
			return strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[")
		}
	}
	return false
}

func prefersDelegateArtifactText(summary, artifact string) bool {
	summary = strings.TrimSpace(summary)
	artifact = strings.TrimSpace(artifact)
	if artifact == "" || looksLikeStructuredTranscript(artifact) {
		return false
	}
	if summary == "" {
		return true
	}
	if strings.Count(artifact, "\n") > strings.Count(summary, "\n") {
		return true
	}
	return len(artifact) > len(summary)
}

func selectDelegateTranscript(summary, artifact string) string {
	summary = strings.TrimSpace(summary)
	artifact = strings.TrimSpace(artifact)
	if prefersDelegateArtifactText(summary, artifact) {
		return artifact
	}
	if summary != "" {
		return summary
	}
	return artifact
}

func (m *ChatModel) refreshViewport() {
	m.resizeChatViewport()
	contentWidth := m.chatContentWidth()
	if contentWidth < 10 {
		contentWidth = 60
	}
	theme := m.theme()
	prevTotalLines := strings.Count(m.chatVisible, "\n") + 1
	if prevTotalLines == 0 {
		prevTotalLines = 1
	}
	prevMaxScroll := max(0, prevTotalLines-max(1, m.chatViewport.Height))
	wasPinnedToBottom := m.followMode == followManual && m.chatViewport.YOffset >= prevMaxScroll

	var blocks []string
	renderedCount := 0
	messageBlockIndex := make([]int, len(m.messages))
	for i := range messageBlockIndex {
		messageBlockIndex[i] = -1
	}
	start, end := 0, len(m.messages)
	if len(m.messages) > transcriptVirtualizationThreshold {
		offset := m.chatViewport.YOffset
		if m.followMode == followBottom || offset <= 0 {
			offset = max(0, len(m.messages)-max(1, m.chatViewport.Height))
		}
		start, end = transcriptVirtualWindow(len(m.messages), offset, max(1, m.chatViewport.Height), transcriptVirtualizationOverscan)
	}
	for i := start; i < end; i++ {
		msg := m.messages[i]
		// Active working status is rendered in the dedicated live slot near the
		// composer; hiding it here avoids duplicate "mirror" lines.
		if msg.Kind == MsgWorking && i == m.recentActivityIndex {
			continue
		}
		// Skip agent/forge boxes with no content — they render as blank space
		// (created before first token arrives; if error occurs, they stay empty)
		if (msg.Kind == MsgAgent || msg.Kind == MsgForge) && strings.TrimSpace(msg.Content) == "" {
			continue
		}
		messageBlockIndex[i] = len(blocks)
		rendered, ok := "", false
		if msg.Kind != MsgWorking {
			rendered, ok = m.renderCache.Get(msg, contentWidth, m.themeID)
		}
		if !ok {
			rendered = msg.Render(contentWidth, theme)
			if msg.Kind != MsgWorking {
				m.renderCache.Put(msg, contentWidth, m.themeID, rendered)
			}
			renderedCount++
		}
		blocks = append(blocks, rendered)
	}
	content := ""
	if len(blocks) > 0 {
		separator := strings.Repeat(" ", contentWidth)
		content = strings.Join(blocks, "\n"+separator+"\n")
	}
	visible := content
	m.chatContent = content
	m.chatVisible = visible
	m.lastRenderStats = transcriptRenderStats{Rendered: renderedCount, Hits: m.renderCache.Hits, Misses: m.renderCache.Misses, Lines: strings.Count(visible, "\n") + 1}
	m.chatViewport.SetContent(visible)
	totalLines := strings.Count(visible, "\n") + 1
	if totalLines == 0 {
		totalLines = 1
	}
	if wasPinnedToBottom {
		m.followMode = followBottom
	}
	m.applyViewportFollow(totalLines)
}

func (m ChatModel) composer() ChatComposer {
	composer := NewChatComposer()
	minLines := 3
	maxLines := chatComposerMaxBodyLines
	if m.height > 0 && m.height < 14 {
		minLines = 2
		maxLines = 10
	}
	composer.SetLineBudget(minLines, maxLines)
	composer.SetText(m.inputBuf)
	composer.SetCursor(m.inputPos)
	composer.SetAttachments(m.attachments)
	composer.SetWorkDir(m.workDir)
	return composer
}

func (m ChatModel) inputHeight() int {
	if m.pendingApproval != nil {
		return m.approvalOverlayHeight()
	}
	return m.composer().Height(m.width)
}

func (m ChatModel) normalModeStatsFooterHeight() int {
	if !m.shouldShowNormalModeStatsFooter() {
		return 0
	}
	return 2
}

// liveStatusSlotHeight includes one blank gap row above the status line so
// the spinner isn't cramped against the chat transcript.
func (m ChatModel) liveStatusSlotHeight() int {
	if n := len(m.liveProgress.Entries); n > 0 {
		return 1 + min(n, 3)
	}
	// Reserve a slot even when empty so the layout doesn't jump
	return 2
}

func (m ChatModel) shouldShowPendingInputPreview() bool {
	if len(m.pendingQueuedInput) == 0 {
		return false
	}
	if m.height <= 0 {
		return true
	}
	minRows := m.headerHeight() + chatHeaderGapHeight + m.pendingInputPreviewContentHeight() + m.liveStatusSlotHeight() + m.inputHeight() + 1
	return minRows <= m.height
}

func (m ChatModel) pendingInputPreviewHeight() int {
	if !m.shouldShowPendingInputPreview() {
		return 0
	}
	return m.pendingInputPreviewContentHeight()
}

func (m ChatModel) pendingInputPreviewContentHeight() int {
	lines := 1 + min(3, len(m.pendingQueuedInput))
	if len(m.pendingQueuedInput) > 3 {
		lines++
	}
	return lines + 2
}

func (m ChatModel) shouldShowNormalModeStatsFooter() bool {
	if m.debugSurfaceActive() || m.height > 0 && m.height < 14 {
		return false
	}
	if m.renderNormalModeStatsLine(m.theme()) == "" {
		return false
	}
	if m.height > 0 {
		minRows := m.headerHeight() + chatHeaderGapHeight + m.debugDockHeight() + m.pendingInputPreviewHeight() + m.liveStatusSlotHeight() + m.inputHeight() + 1 + 2
		if minRows > m.height {
			return false
		}
	}
	return true
}

func (m ChatModel) debugSurfaceActive() bool {
	surfaceKind := m.config.SurfaceKind
	if surfaceKind == "" {
		surfaceKind = ChatSurfaceDefault
	}
	return surfaceKind == ChatSurfaceDebug
}

func (m ChatModel) debugDockHeight() int {
	if !m.debugSurfaceActive() {
		return 0
	}
	return chatDebugDockHeight
}

func (m ChatModel) composerGapHeight() int {
	return chatComposerGapHeight
}

func (m ChatModel) headerHeight() int {
	if m.width <= 0 {
		return 1
	}
	header := renderStatusHeaderForHeight(m.theme(), m.statusSnapshot(), m.width, m.height)
	height := strings.Count(header, "\n") + 1
	if height < 1 {
		return 1
	}
	return height
}

type normalChatLayoutBudget struct {
	Header      int
	HeaderGap   int
	Chat        int
	DebugDock   int
	Pending     int
	LiveStatus  int
	TaskPanel   int
	ToolCards   int
	FileChanges int
	Input       int
	StatsFooter int
	Total       int
}

func (m ChatModel) normalChatLayoutBudget() normalChatLayoutBudget {
	b := normalChatLayoutBudget{
		Header:      m.headerHeight(),
		HeaderGap:   chatHeaderGapHeight,
		DebugDock:   m.debugDockHeight(),
		TaskPanel:   m.agentTaskPanelHeight(),
		ToolCards:   m.toolCardsPanelHeight(),
		FileChanges: m.fileChangesPanelHeight(),
		Pending:     m.pendingInputPreviewHeight(),
		LiveStatus:  m.liveStatusSlotHeight(),
		Input:       m.inputHeight(),
		StatsFooter: m.normalModeStatsFooterHeight(),
	}
	b.Chat = max(1, m.height-b.Header-b.HeaderGap-b.DebugDock-b.TaskPanel-b.ToolCards-b.FileChanges-b.Pending-b.LiveStatus-b.Input-b.StatsFooter)
	b.Total = b.Header + b.HeaderGap + b.Chat + b.DebugDock + b.TaskPanel + b.ToolCards + b.FileChanges + b.Pending + b.LiveStatus + b.Input + b.StatsFooter
	return b
}

func (m *ChatModel) resizeChatViewport() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.chatViewport.Width = m.chatContentWidth()
	bodyH := m.normalChatLayoutBudget().Chat
	if m.chatViewport.Height == bodyH {
		return
	}
	m.chatViewport.Height = bodyH
	totalLines := 1
	if strings.TrimSpace(m.chatVisible) != "" {
		totalLines = strings.Count(m.chatVisible, "\n") + 1
	}
	m.applyViewportFollow(totalLines)
}

func (m ChatModel) chatPaneWidth() int {
	return m.width
}

func (m ChatModel) hasAgentWorkPane() bool {
	return false
}

func (m ChatModel) hasAgentWorkPaneContent() bool {
	if strings.TrimSpace(m.renderedAgentTaskStateBuf(time.Now())) != "" {
		return true
	}
	for _, sec := range m.toolsSections {
		if strings.TrimSpace(sec.role) == "" {
			continue
		}
		if strings.TrimSpace(sec.buf) != "" || strings.TrimSpace(sec.summary) != "" {
			return true
		}
	}
	return false
}

func (m ChatModel) chatContentWidth() int {
	paneWidth := m.chatPaneWidth()
	return max(10, paneWidth-1)
}

type chatLayoutMouseContext struct {
	chatX, chatY, chatW, chatH     int
	toolsX, toolsY, toolsW, toolsH int
	inputY                         int
	inChat, inTools                bool
	inChatScrollbar                bool
	inToolsScrollbar               bool
}

func (m ChatModel) mouseContext() chatLayoutMouseContext {
	headerH := m.headerHeight()
	chatPaneWidth := m.chatPaneWidth()
	chatBodyHeight := max(1, m.chatViewport.Height)
	chatH := chatBodyHeight + chatPaneBorderHeight
	ctx := chatLayoutMouseContext{
		chatX:  0,
		chatY:  headerH + chatHeaderGapHeight,
		chatW:  chatPaneWidth,
		chatH:  chatH,
		inputY: headerH + chatHeaderGapHeight + chatH + m.debugDockHeight() + m.composerGapHeight(),
	}
	if m.hasAgentWorkPane() {
		ctx.toolsX = chatPaneWidth
		ctx.toolsY = headerH + chatHeaderGapHeight
		ctx.toolsW = max(0, m.width-chatPaneWidth)
		ctx.toolsH = chatH
	}
	return ctx
}

func (m *ChatModel) currentToolsSection(role string) *toolsSection {
	if len(m.toolsSections) == 0 || m.toolsSections[len(m.toolsSections)-1].role != role {
		m.toolsSections = append(m.toolsSections, toolsSection{role: role})
	}
	return &m.toolsSections[len(m.toolsSections)-1]
}

func (m *ChatModel) appendTools(role, text string) {
	sec := m.currentToolsSection(role)
	sec.buf += text
}

func (m ChatModel) toolsMaxScroll() int {
	return max(0, len(m.toolsWrappedLines())-max(1, m.chatViewport.Height))
}

func (m ChatModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.helpVisible {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.helpVisible = false
		}
		return m, nil
	}
	if m.statsVisible {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.statsVisible = false
		}
		return m, nil
	}
	if m.searchVisible {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.searchVisible = false
		}
		return m, nil
	}
	if m.filesVisible {
		return m.handleFilesMouse(msg)
	}
	if m.modelsVisible {
		return m.handleModelsMouse(msg)
	}
	if m.providersVisible {
		return m.handleProvidersMouse(msg)
	}
	if m.sessionsVisible {
		return m.handleSessionsMouse(msg)
	}

	ctx := m.mouseContext()
	x, y := msg.X, msg.Y
	ctx.inChat = x >= ctx.chatX && x < ctx.chatX+ctx.chatW && y >= ctx.chatY && y < ctx.chatY+ctx.chatH
	ctx.inTools = ctx.toolsW > 0 && x >= ctx.toolsX && x < ctx.toolsX+ctx.toolsW && y >= ctx.toolsY && y < ctx.toolsY+ctx.toolsH
	inInput := y >= ctx.inputY && y < ctx.inputY+m.inputHeight()
	chatScrollbarX := ctx.chatX + max(1, ctx.chatW-2)
	toolsScrollbarX := ctx.toolsX + max(1, ctx.toolsW-2)
	ctx.inChatScrollbar = ctx.inChat && x == chatScrollbarX && y > ctx.chatY && y < ctx.chatY+ctx.chatH-1
	ctx.inToolsScrollbar = ctx.inTools && x == toolsScrollbarX && y > ctx.toolsY && y < ctx.toolsY+ctx.toolsH-1

	scrollStep := 6
	if tea.MouseEvent(msg).IsWheel() {
		if inInput {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if ctx.inTools {
				m.paneFocus = focusTools
				m.toolsScroll = max(0, m.toolsScroll-scrollStep)
				return m, nil
			}
			m.paneFocus = focusChat
			m.markManualScroll()
			m.chatViewport.ScrollUp(scrollStep)
			return m, nil
		case tea.MouseButtonWheelDown:
			if ctx.inTools {
				m.paneFocus = focusTools
				m.toolsScroll = min(m.toolsMaxScroll(), m.toolsScroll+scrollStep)
				return m, nil
			}
			m.paneFocus = focusChat
			m.markManualScroll()
			m.chatViewport.ScrollDown(scrollStep)
			return m, nil
		}
	}

	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		switch {
		case ctx.inChatScrollbar:
			m.paneFocus = focusChat
			m.markManualScroll()
			total := len(strings.Split(m.chatVisible, "\n"))
			visible := max(1, m.chatViewport.Height)
			thumbTop, thumbH := scrollbarThumb(ctx.chatY+2, max(1, visible-2), total, visible, m.chatViewport.YOffset)
			switch {
			case y == ctx.chatY+1:
				m.chatViewport.ScrollUp(1)
			case y == ctx.chatY+ctx.chatH-2:
				m.chatViewport.ScrollDown(1)
			case y >= thumbTop && y < thumbTop+thumbH:
				// thumb click focuses pane; drag can be added later
			case y < thumbTop:
				m.chatViewport.ScrollUp(max(1, visible-1))
			default:
				m.chatViewport.ScrollDown(max(1, visible-1))
			}
			return m, nil
		case ctx.inToolsScrollbar:
			m.paneFocus = focusTools
			total := len(m.toolsWrappedLines())
			visible := max(1, m.chatViewport.Height)
			thumbTop, thumbH := scrollbarThumb(ctx.toolsY+2, max(1, visible-2), total, visible, m.toolsScroll)
			trackH := max(1, visible-2)
			switch {
			case y == ctx.toolsY+1:
				m.toolsScroll = max(0, m.toolsScroll-scrollStep)
			case y == ctx.toolsY+ctx.toolsH-2:
				m.toolsScroll = min(m.toolsMaxScroll(), m.toolsScroll+scrollStep)
			case y >= thumbTop && y < thumbTop+thumbH:
				// Drag: map click position to scroll position proportionally.
				relY := y - (ctx.toolsY + 2)
				if trackH > 0 && total > visible {
					m.toolsScroll = min(m.toolsMaxScroll(), (relY*(total-visible))/trackH)
				}
			case y < thumbTop:
				m.toolsScroll = max(0, m.toolsScroll-(visible-1))
			default:
				m.toolsScroll = min(m.toolsMaxScroll(), m.toolsScroll+(visible-1))
			}
			_ = thumbH
			return m, nil
		case ctx.inTools:
			m.paneFocus = focusTools
			return m, nil
		case ctx.inChat:
			m.paneFocus = focusChat
			return m, nil
		}
	}

	var cmd tea.Cmd
	if m.paneFocus != focusTools {
		m.chatViewport, cmd = m.chatViewport.Update(msg)
	}
	return m, cmd
}

func (m ChatModel) modelsOverlayLayout() (x0, y0, boxW, boxH, listY, contentHeight, start int) {
	boxW = min(96, max(56, m.width-6))
	boxH = min(28, max(14, m.height-4))
	contentHeight = max(1, boxH-8)
	x0 = max(0, (m.width-boxW)/2)
	y0 = max(0, (m.height-boxH)/2)
	listY = y0 + 5
	if m.modelsCursor >= contentHeight {
		start = m.modelsCursor - contentHeight + 1
	}
	return
}

func (m ChatModel) providersOverlayLayout() (x0, y0, boxW, boxH, listY, contentHeight, start int) {
	boxW = min(96, max(64, m.width-6))
	boxH = min(30, max(14, m.height-4))
	contentHeight = max(1, boxH-9)
	x0 = max(0, (m.width-boxW)/2)
	y0 = max(0, (m.height-boxH)/2)
	listY = y0 + 5
	if m.providersCursor >= contentHeight {
		start = m.providersCursor - contentHeight + 1
	}
	return
}

func (m ChatModel) sessionsOverlayLayout() (x0, y0, boxW, boxH, listY, contentHeight, start int) {
	boxW = min(88, max(56, m.width-6))
	boxH = min(28, max(12, m.height-4))
	contentHeight = max(1, boxH-6)
	x0 = max(0, (m.width-boxW)/2)
	y0 = max(0, (m.height-boxH)/2)
	listY = y0 + 4
	if m.sessionsCursor >= contentHeight {
		start = m.sessionsCursor - contentHeight + 1
	}
	return
}

func (m ChatModel) filesOverlayLayout() (x0, y0, boxW, boxH, listY, contentHeight, start int) {
	boxW = min(96, max(42, m.width-6))
	boxH = min(30, max(12, m.height-4))
	contentHeight = max(1, boxH-8)
	x0 = max(0, (m.width-boxW)/2)
	y0 = max(0, (m.height-boxH)/2)
	listY = y0 + 5
	if m.filesCursor >= contentHeight {
		start = m.filesCursor - contentHeight + 1
	}
	return
}

func (m ChatModel) handleModelsMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	x0, y0, boxW, boxH, listY, contentHeight, start := m.modelsOverlayLayout()
	if tea.MouseEvent(msg).IsWheel() {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.modelsCursor > 0 {
				m.modelsCursor--
			}
		case tea.MouseButtonWheelDown:
			if m.modelsCursor < len(m.modelsFiltered)-1 {
				m.modelsCursor++
			}
		}
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if msg.X >= x0 && msg.X < x0+boxW && msg.Y >= y0 && msg.Y < y0+boxH {
			if msg.Y >= listY && msg.Y < listY+contentHeight {
				idx := start + (msg.Y - listY)
				if idx >= 0 && idx < len(m.modelsFiltered) {
					m.modelsCursor = idx
					return m, m.pickModel(idx)
				}
			}
			return m, nil
		}
		m.modelsVisible = false
	}
	return m, nil
}

func (m ChatModel) handleProvidersMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.providerAuthWaiting || m.providerPromptingKey {
		// ponytail: mouse tracking swallows OSC 8 clicks, so any click during
		// auth opens the URL instead of selecting/closing underneath it
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && strings.TrimSpace(m.providerAuthURL) != "" {
			return m, openProviderAuthURL(m.providerAuthURL)
		}
		return m, nil
	}
	x0, y0, boxW, boxH, listY, contentHeight, start := m.providersOverlayLayout()
	if tea.MouseEvent(msg).IsWheel() {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.providersCursor > 0 {
				m.providersCursor--
			}
		case tea.MouseButtonWheelDown:
			if m.providersCursor < len(m.providersList)-1 {
				m.providersCursor++
			}
		}
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if msg.X >= x0 && msg.X < x0+boxW && msg.Y >= y0 && msg.Y < y0+boxH {
			if msg.Y >= listY && msg.Y < listY+contentHeight {
				idx := start + (msg.Y - listY)
				if idx >= 0 && idx < len(m.providersList) {
					m.providersCursor = idx
					return m.activateProviderSelection()
				}
			}
			return m, nil
		}
		m.providersVisible = false
	}
	return m, nil
}

func (m ChatModel) handleSessionsMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.sessionRenaming {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.sessionRenaming = false
		}
		return m, nil
	}
	x0, y0, boxW, boxH, listY, contentHeight, start := m.sessionsOverlayLayout()
	if tea.MouseEvent(msg).IsWheel() {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.sessionsCursor > 0 {
				m.sessionsCursor--
			}
		case tea.MouseButtonWheelDown:
			if m.sessionsCursor < len(m.sessionsList)-1 {
				m.sessionsCursor++
			}
		}
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if msg.X >= x0 && msg.X < x0+boxW && msg.Y >= y0 && msg.Y < y0+boxH {
			if msg.Y >= listY && msg.Y < listY+contentHeight {
				idx := start + (msg.Y - listY)
				if idx >= 0 && idx < len(m.sessionsList) {
					m.sessionsCursor = idx
					m.restorePickedSession(idx)
				}
			}
			return m, nil
		}
		m.sessionsVisible = false
	}
	return m, nil
}

func (m ChatModel) handleFilesMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.filesViewing {
		if tea.MouseEvent(msg).IsWheel() {
			_, _, _, _, _, contentHeight, _ := m.filesOverlayLayout()
			maxScroll := max(0, len(strings.Split(m.filesViewText, "\n"))-contentHeight)
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.filesViewScroll = max(0, m.filesViewScroll-3)
			case tea.MouseButtonWheelDown:
				m.filesViewScroll = min(maxScroll, m.filesViewScroll+3)
			}
			return m, nil
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			x0, y0, boxW, boxH, _, _, _ := m.filesOverlayLayout()
			if !(msg.X >= x0 && msg.X < x0+boxW && msg.Y >= y0 && msg.Y < y0+boxH) {
				m.filesVisible = false
				m.filesViewing = false
			}
		}
		return m, nil
	}
	x0, y0, boxW, boxH, listY, contentHeight, start := m.filesOverlayLayout()
	if tea.MouseEvent(msg).IsWheel() {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.filesCursor > 0 {
				m.filesCursor--
			}
		case tea.MouseButtonWheelDown:
			if m.filesCursor < len(m.filesFiltered)-1 {
				m.filesCursor++
			}
		}
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if msg.X >= x0 && msg.X < x0+boxW && msg.Y >= y0 && msg.Y < y0+boxH {
			if msg.Y >= listY && msg.Y < listY+contentHeight {
				idx := start + (msg.Y - listY)
				if idx >= 0 && idx < len(m.filesFiltered) {
					m.filesCursor = idx
					if m.filesBrowser {
						m.openFileViewer(m.filesFiltered[idx])
					} else {
						m.replaceActiveAtToken(m.filesFiltered[idx])
					}
				}
			}
			return m, nil
		}
		m.filesVisible = false
	}
	return m, nil
}

func (m ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeChatViewport()
		m.refreshViewport()
		return m, nil

	case chatTickMsg:
		if m.busy {
			m.spinnerFrame = (m.spinnerFrame + 1) % 8
		}
		// Detect tools pane appearing/disappearing and resize chat viewport
		toolsShowing := m.toolsVisible
		if toolsShowing != m.toolsWasShowing {
			m.toolsWasShowing = toolsShowing
			m.chatViewport.Width = m.chatContentWidth()
			m.viewportDirty = true
		}
		if m.viewportDirty {
			m.refreshViewport()
			m.viewportDirty = false
		}
		return m, tickCmd()

	case llm.Event:
		return m.handleLLMEvent(msg)

	case chatApprovalMsg:
		action := tools.Action(msg)
		m.pendingApproval = &action
		return m, nil

	case pluginCommandResultMsg:
		header := "Plugin Result"
		content := msg.content
		if msg.err != nil {
			header = "Plugin Error"
			if content != "" {
				content += "\n" + msg.err.Error()
			} else {
				content = msg.err.Error()
			}
		}
		if content != "" {
			m.AddMessage(ChatMessage{Kind: MsgForge, Header: header, Content: content})
		}
		return m, nil

	case providerAuthStartedMsg:
		return m.handleProviderAuthStarted(msg)

	case providerAuthSucceededMsg:
		return m.handleProviderAuthSucceeded(msg)

	case providerAuthFailedMsg:
		return m.handleProviderAuthFailed(msg)

	case providerAuthURLOpenedMsg:
		m.providerStatus = "opened browser"
		return m, nil

	case providerAuthURLOpenFailedMsg:
		if msg.err != nil {
			m.providerStatus = fmt.Sprintf("open failed: %v", msg.err)
		} else {
			m.providerStatus = "open failed"
		}
		return m, nil

	case statsCopilotQuotaMsg:
		if msg.model != m.model {
			return m, nil
		}
		m.statsCopilotLoading = false
		m.statusData.CopilotLive = msg.quota
		if msg.err != nil {
			m.statsCopilotErr = msg.err.Error()
		} else {
			m.statsCopilotErr = ""
		}
		return m, nil

	case statsCodexUsageMsg:
		if msg.model != m.model {
			return m, nil
		}
		m.statsCodexLoading = false
		m.statusData.CodexUsage = msg.snapshot
		if msg.err != nil {
			m.statsCodexErr = msg.err.Error()
		} else {
			m.statsCodexErr = ""
		}
		return m, nil

	case tea.KeyMsg:
		if m.discardMouseCSI {
			if isMouseTrackingFragment(msg) {
				m.discardMouseCSI = false
				return m, nil
			}
			m.discardMouseCSI = false
		}
		if startsMouseTrackingSequence(msg) {
			m.discardMouseCSI = true
			return m, nil
		}
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}

	var cmd tea.Cmd
	m.chatViewport, cmd = m.chatViewport.Update(msg)
	return m, cmd
}

func (m ChatModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.helpVisible {
		return m.handleHelpKey(msg)
	}
	if m.statsVisible {
		return m.handleStatsKey(msg)
	}
	if m.traceVisible {
		return m.handleTraceKey(msg)
	}
	if m.searchVisible {
		return m.handleSearchKey(msg)
	}
	if m.filesVisible {
		return m.handleFilePickerKey(msg)
	}
	if m.modelsVisible {
		return m.handleModelsKey(msg)
	}
	if m.providersVisible {
		return m.handleProvidersKey(msg)
	}
	if m.sessionsVisible {
		return m.handleSessionsKey(msg)
	}
	if m.agentViewVisible {
		switch msg.Type {
		case tea.KeyEscape:
			m.agentViewVisible = false
			m.flash = "agent view closed"
			return m, nil
		case tea.KeyTab:
			m.cycleAgentView(1)
			return m, nil
		case tea.KeyShiftTab:
			m.cycleAgentView(-1)
			return m, nil
		case tea.KeyPgUp:
			m.toolsScroll = clamp(m.toolsScroll-max(1, m.chatViewport.Height/2), 0, m.agentViewMaxScroll())
			return m, nil
		case tea.KeyPgDown:
			m.toolsScroll = clamp(m.toolsScroll+max(1, m.chatViewport.Height/2), 0, m.agentViewMaxScroll())
			return m, nil
		case tea.KeyUp:
			m.toolsScroll = clamp(m.toolsScroll-1, 0, m.agentViewMaxScroll())
			return m, nil
		case tea.KeyDown:
			m.toolsScroll = clamp(m.toolsScroll+1, 0, m.agentViewMaxScroll())
			return m, nil
		}
	}

	// Handle approval mode
	if m.pendingApproval != nil {
		switch {
		case msg.Type == tea.KeyRunes && string(msg.Runes) == "y":
			m.pendingApproval = nil
			if m.responseCh != nil {
				ch := m.responseCh
				return m, func() tea.Msg {
					ch <- true
					return nil
				}
			}
		case msg.Type == tea.KeyRunes && string(msg.Runes) == "n":
			m.pendingApproval = nil
			if m.responseCh != nil {
				ch := m.responseCh
				return m, func() tea.Msg {
					ch <- false
					return nil
				}
			}
		case msg.Type == tea.KeyCtrlC:
			return m, m.quitCmd()
		}
		return m, nil
	}

	switch msg.Type {
	case tea.KeyCtrlF:
		m.openSearchOverlay("")
		return m, nil
	case tea.KeyCtrlT:
		m.setToolPanelsVisible(!m.toolPanelsVisible)
		return m, nil
	case tea.KeyEscape:
		if m.busy && m.inputCh != nil {
			ch := m.inputCh
			m.lastEscapeTime = time.Now()
			m.flash = "canceling..."
			return m, func() tea.Msg {
				ch <- "__cancel_turn__"
				return nil
			}
		}
		m.resetSlashCompletion()
		return m, nil
	case tea.KeyCtrlC:
		if !m.busy && strings.TrimSpace(m.inputBuf) == "" {
			return m, m.quitCmd()
		}
	case tea.KeyEnter:
	case tea.KeyPgUp:
		m.resetSlashCompletion()
		m.markManualScroll()
		m.chatViewport.HalfPageUp()
	case tea.KeyPgDown:
		m.resetSlashCompletion()
		m.markManualScroll()
		m.chatViewport.HalfPageDown()
	case tea.KeyF1:
		m.helpVisible = true
		m.helpTab = 0
		m.helpScroll = 0
		m.flash = ""
		return m, nil
	case tea.KeyTab:
		if m.completeSlashCommand() {
			return m, nil
		}
		return m, nil
	}

	if msg.Type == tea.KeyRunes {
		m.flash = ""
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'n':
				if m.searchNext(1) {
					return m, nil
				}
			case 'N':
				if m.searchNext(-1) {
					return m, nil
				}
			}
		}
	}

	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyCtrlD, tea.KeyEnter, tea.KeyBackspace, tea.KeyLeft, tea.KeyRight, tea.KeyHome, tea.KeyEnd, tea.KeySpace, tea.KeyRunes:
		if msg.Type == tea.KeyEnter || msg.Type == tea.KeyBackspace || msg.Type == tea.KeyLeft || msg.Type == tea.KeyRight || msg.Type == tea.KeyHome || msg.Type == tea.KeyEnd || msg.Type == tea.KeySpace || msg.Type == tea.KeyRunes {
			m.resetSlashCompletion()
		}
		if msg.Type == tea.KeyEnter || msg.Type == tea.KeySpace || msg.Type == tea.KeyRunes {
			m.flash = ""
		}

		prevText := m.inputBuf
		prevPos := m.inputPos
		prevAttachments := len(m.attachments)
		composer := m.composer()
		action := composer.HandleKey(msg, m.busy)
		if action.SubmitText == "" && !action.CancelTurn && !action.Exit && len(action.Attachments) == 0 && composer.Text() == prevText && composer.Cursor() == prevPos && len(composer.Attachments()) == prevAttachments {
			return m, nil
		}

		m.inputBuf = composer.Text()
		m.inputPos = composer.Cursor()
		m.attachments = composer.Attachments()
		m.resizeChatViewport()

		switch {
		case action.SubmitText != "" || len(action.Attachments) > 0:
			m.attachments = action.Attachments
			updated, cmd, submitted := m.trySubmitText(action.SubmitText, action.Attachments)
			m = updated
			if !submitted {
				m.inputBuf = prevText
				m.inputPos = prevPos
				m.resizeChatViewport()
			}
			return m, cmd
		case action.CancelTurn:
			if m.inputCh != nil {
				ch := m.inputCh
				m.flash = "canceling..."
				return m, func() tea.Msg {
					ch <- "__cancel_turn__"
					return nil
				}
			}
			return m, nil
		case action.Exit:
			return m, m.quitCmd()
		default:
			if msg.Type == tea.KeyRunes && !msg.Paste {
				for _, r := range msg.Runes {
					if r == '@' {
						m.openFilePicker("")
						break
					}
				}
			}
			return m, nil
		}
	}
	return m, nil
}

func (m ChatModel) trySubmitText(input string, attachments []chatstate.ChatAttachment) (ChatModel, tea.Cmd, bool) {
	input = strings.TrimSpace(input)
	if input == "" && len(attachments) == 0 {
		return m, nil, false
	}

	if input == "/exit" || input == "/quit" {
		return m, m.quitCmd(), true
	}

	if strings.HasPrefix(input, "/") {
		if len(attachments) > 0 {
			m.flash = "cannot combine commands with image attachments — remove attachments first"
			return m, nil, false
		}
		cmd := strings.TrimPrefix(input, "/")
		if m.isBuiltinCommand(input) {
			updated, submitCmd := m.handleSlashCommand(input)
			return updated.(ChatModel), submitCmd, true
		}
		if s, ok := skills.Get(m.skills, cmd); ok {
			updated, submitCmd := m.submitSkillInput(s, fmt.Sprintf("/%s", s.Name), "")
			return updated.(ChatModel), submitCmd, true
		}
		// Check plugin commands (Name may be "sandbox" or "/sandbox")
		if pluginCommands := plugin.Global().GetAllCommands(); len(pluginCommands) > 0 {
			for _, pluginCmd := range pluginCommands {
				cmdName := strings.TrimPrefix(pluginCmd.Name, "/")
				// Match exact command name (first word after /), allowing trailing args
				if input == "/"+cmdName || strings.HasPrefix(input, "/"+cmdName+" ") || strings.HasPrefix(input, "/"+cmdName+"\t") {
					// Run plugin command async; result re-enters via Update so
					// the message is appended to the live model, not a copy.
					args := ""
					if idx := strings.Index(input, " "); idx != -1 {
						args = input[idx+1:]
					}
					pc := pluginCmd
					m.AddMessage(ChatMessage{
						Kind:    MsgStatus,
						Content: fmt.Sprintf("running %s ...", pc.Name),
					})
					m.inputBuf = ""
					m.inputPos = 0
					return m, func() tea.Msg {
						result, err := pc.Handler(context.Background(), args)
						return pluginCommandResultMsg{content: result, err: err}
					}, true
				}
			}
		}
		if looksLikeAbsolutePathInput(input) {
			goto submitChatInput
		}
		updated, submitCmd := m.handleSlashCommand(input)
		return updated.(ChatModel), submitCmd, true
	}

submitChatInput:

	if m.busy {
		stamp := time.Now().Format("15:04:05")
		displayText := input
		if len(attachments) > 0 {
			names := make([]string, len(attachments))
			for i, att := range attachments {
				names[i] = att.Name
			}
			displayText = fmt.Sprintf("[%s] %s", strings.Join(names, ", "), input)
		}
		m.AddMessage(ChatMessage{
			Kind:    MsgUser,
			Header:  "You • " + stamp,
			Content: displayText,
		})
		m.flash = "queued steering"
		m.pendingQueuedInput = append(m.pendingQueuedInput, input)
		m.inputBuf = ""
		m.inputPos = 0
		if m.inputCh != nil {
			ch := m.inputCh
			ui := chatstate.ChatUserInput{IsInput: true, Text: input, Attachments: attachments}
			return m, func() tea.Msg {
				ch <- chatUserInputToString(ui)
				return nil
			}, true
		}
		return m, nil, true
	}

	if strings.TrimSpace(m.model) == "" {
		m.flash = "configure a provider first with /provider, then pick a model with /models"
		return m, nil, false
	}

	// Model capability check for images
	if len(attachments) > 0 && m.config.ModelInfo != nil {
		info := m.config.ModelInfo(m.model)
		if info != nil && !info.SupportsImages {
			m.flash = "current model may not support image input — try gpt-4o or similar"
		}
	}

	stamp := time.Now().Format("15:04:05")
	displayText := input
	if len(attachments) > 0 {
		names := make([]string, len(attachments))
		for i, att := range attachments {
			names[i] = att.Name
		}
		displayText = fmt.Sprintf("[%s] %s", strings.Join(names, ", "), input)
	}
	m.AddMessage(ChatMessage{
		Kind:    MsgUser,
		Header:  "You • " + stamp,
		Content: displayText,
	})
	m.anchorLatestTurnToBottom()
	m.refreshViewport()
	m.resetProgressCheckpointState()

	m.busy = true
	m.statsDuration = 0
	m.statsUsage = llm.Usage{}
	m.liveStatsStartedAt = time.Now()
	m.liveStatsOutputChars = 0
	m.status = "running"
	m.syncStatusData()

	m.inputBuf = ""
	m.inputPos = 0
	m.attachments = nil

	if m.inputCh != nil {
		ch := m.inputCh
		ui := chatstate.ChatUserInput{IsInput: true, Text: input, Attachments: attachments}
		return m, func() tea.Msg {
			ch <- chatUserInputToString(ui)
			return nil
		}, true
	}

	return m, nil, true
}

func chatUserInputToString(ui chatstate.ChatUserInput) string {
	data, _ := json.Marshal(ui)
	return string(data)
}

func (m ChatModel) submitInput() (tea.Model, tea.Cmd) {
	updated, cmd, submitted := m.trySubmitText(m.inputBuf, m.attachments)
	m = updated
	if submitted {
		m.inputBuf = ""
		m.inputPos = 0
		m.attachments = nil
		m.resizeChatViewport()
	}
	return m, cmd
}

func (m *ChatModel) resetSlashCompletion() {
	m.slashComplete = chatSlashCompletionState{}
}

func (m *ChatModel) completeSlashCommand() bool {
	if strings.Contains(m.inputBuf, "\n") {
		m.resetSlashCompletion()
		return false
	}
	input := strings.TrimSpace(m.inputBuf)
	if !strings.HasPrefix(input, "/") {
		m.resetSlashCompletion()
		return false
	}
	if m.slashComplete.baseInput != "" && len(m.slashComplete.matches) > 1 && input == m.inputBuf {
		for _, match := range m.slashComplete.matches {
			if input == match {
				m.slashComplete.index = (m.slashComplete.index + 1) % len(m.slashComplete.matches)
				m.inputBuf = m.slashComplete.matches[m.slashComplete.index]
				m.inputPos = len([]rune(m.inputBuf))
				m.flash = strings.Join(m.slashComplete.matches, "  ")
				return true
			}
		}
	}
	matches := m.matchingSlashCommands(input)
	if len(matches) == 0 {
		m.resetSlashCompletion()
		m.flash = fmt.Sprintf("no command matches %s", input)
		return true
	}
	if len(matches) == 1 {
		m.resetSlashCompletion()
		m.inputBuf = matches[0]
		m.inputPos = len([]rune(m.inputBuf))
		m.flash = matches[0]
		return true
	}
	prefix := longestCommonPrefix(matches)
	m.slashComplete = chatSlashCompletionState{baseInput: input, matches: matches, index: 0}
	if len([]rune(prefix)) > len([]rune(input)) {
		m.inputBuf = prefix
		m.inputPos = len([]rune(m.inputBuf))
		m.flash = strings.Join(matches, "  ")
		return true
	}
	m.inputBuf = matches[0]
	m.inputPos = len([]rune(m.inputBuf))
	m.flash = strings.Join(matches, "  ")
	return true
}

func (m ChatModel) matchingSlashCommands(input string) []string {
	matches := make([]string, 0)
	for _, cmd := range builtinCommands {
		if strings.HasPrefix(cmd, input) {
			matches = append(matches, cmd)
		}
	}
	// include plugin-registered slash commands
	if pluginCommands := plugin.Global().GetAllCommands(); len(pluginCommands) > 0 {
		for _, pc := range pluginCommands {
			if strings.HasPrefix(pc.Name, input) {
				matches = append(matches, pc.Name)
			}
		}
	}
	return matches
}

func longestCommonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) && prefix != "" {
			prefix = prefix[:len(prefix)-1]
		}
		if prefix == "" {
			return ""
		}
	}
	return prefix
}

func uniqueStringsPreserveOrder(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func resolveModelName(models []string, input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	var idx int
	if _, err := fmt.Sscanf(input, "%d", &idx); err == nil && idx >= 1 && idx <= len(models) {
		return models[idx-1]
	}
	for _, model := range models {
		if strings.EqualFold(model, input) {
			return model
		}
	}
	for _, model := range models {
		if strings.Contains(strings.ToLower(model), strings.ToLower(input)) {
			return model
		}
	}
	return ""
}

func (m ChatModel) submitSkillInput(s skills.Skill, turnLabel, msg string) (tea.Model, tea.Cmd) {
	if m.state != nil && m.state.SkillActivated(s.Name) {
		m.flash = fmt.Sprintf("skill already active: %s", s.Name)
		return m, nil
	}
	if m.busy {
		m.flash = fmt.Sprintf("busy — skill %s queued", s.Name)
		return m, nil
	}
	if m.state != nil {
		m.state.ActivateSkill(s.Name)
	}
	m.AddMessage(ChatMessage{
		Kind:    MsgStatus,
		Content: fmt.Sprintf("skill activated: %s", s.Name),
	})
	stamp := time.Now().Format("15:04:05")
	m.AddMessage(ChatMessage{
		Kind:    MsgForge,
		Header:  "Forge • " + stamp,
		Content: turnLabel,
	})
	m.anchorLatestTurnToBottom()
	m.refreshViewport()

	m.inputBuf = ""
	m.inputPos = 0
	m.flash = fmt.Sprintf("skill: %s", s.Name)
	m.resetProgressCheckpointState()
	m.busy = true
	m.statsDuration = 0
	m.statsUsage = llm.Usage{}
	m.liveStatsStartedAt = time.Now()
	m.liveStatsOutputChars = 0
	m.status = "running"
	m.syncStatusData()

	if m.inputCh != nil {
		ch := m.inputCh
		return m, func() tea.Msg {
			ch <- chatUserInputToString(chatstate.ChatUserInput{IsInput: true, Text: msg, SkillName: s.Name, SkillBody: s.Body})
			return nil
		}
	}
	return m, nil
}

var builtinCommands = []string{
	"/new", "/clear", "/clear all", "/clear agent", "/clear tools",
	"/help", "/stats", "/trace",
	"/theme", "/theme low", "/theme default", "/theme light", "/theme dusk", "/theme midnight-ink", "/theme eclipse",
	"/agentview",
	"/tools", "/toggle tools", "/toggle tools on", "/toggle tools off",
	"/models", "/model", "/provider",
	"/skills", "/sessions", "/save", "/restore", "/remember",
	"/find", "/files", "/copy agent", "/copy tools", "/copy code", "/copy result",
	"/make", "/exit", "/quit",
	"/reload",
}

func (m ChatModel) isBuiltinCommand(input string) bool {
	for _, cmd := range builtinCommands {
		if input == cmd || strings.HasPrefix(input, cmd+" ") {
			return true
		}
	}
	return false
}

func looksLikeAbsolutePathInput(input string) bool {
	candidate := strings.Trim(strings.TrimSpace(input), "'\"")
	if !filepath.IsAbs(candidate) {
		return false
	}
	withoutRoot := strings.TrimPrefix(candidate, string(filepath.Separator))
	if strings.ContainsRune(withoutRoot, filepath.Separator) {
		return true
	}
	if _, err := os.Stat(candidate); err == nil {
		return true
	}
	return false
}

func (m ChatModel) handleSlashCommand(input string) (tea.Model, tea.Cmd) {
	m.inputBuf = ""
	m.inputPos = 0

	switch {
	case input == "/new":
		m.messages = nil
		m.resetRecentActivity()
		m.clearToolsSections()
		m.turnAnchorMessageIndex = -1
		m.pendingQueuedInput = nil
		m.followMode = followBottom
		m.refreshViewport()
		if m.config.ClearHistory != nil {
			m.config.ClearHistory()
		}
		m.flash = "new session started"
	case input == "/clear" || input == "/clear all":
		m.messages = nil
		m.resetRecentActivity()
		m.clearToolsSections()
		m.turnAnchorMessageIndex = -1
		m.pendingQueuedInput = nil
		m.followMode = followBottom
		m.refreshViewport()
		if m.config.ClearHistory != nil {
			m.config.ClearHistory()
		}
		m.flash = "conversation cleared"
	case input == "/clear agent":
		m.messages = nil
		m.resetRecentActivity()
		m.turnAnchorMessageIndex = -1
		m.followMode = followBottom
		m.refreshViewport()
		m.flash = "conversation cleared"
	case input == "/clear tools":
		m.clearToolsSections()
		m.flash = "tools pane cleared"
	case input == "/help":
		m.helpVisible = true
		m.helpTab = 0
		m.helpScroll = 0
		m.flash = "help opened"
	case input == "/stats":
		m.statsVisible = true
		m.flash = "stats opened"
		return m, m.beginProviderDiagnosticsFetch(false)
	case input == "/trace":
		if !m.debugEnabled {
			m.flash = "trace unavailable without -d"
			break
		}
		m.traceVisible = !m.traceVisible
		if m.traceVisible {
			m.flash = "trace opened"
		} else {
			m.flash = "trace closed"
		}
	case input == "/theme":
		m.cycleTheme()
	case strings.HasPrefix(input, "/theme "):
		name := strings.TrimSpace(strings.TrimPrefix(input, "/theme "))
		if name == "" {
			m.flash = "unknown theme \"\""
			break
		}
		m.applyTheme(name)
	case input == "/agentview":
		if !m.hasAgentWorkPaneContent() {
			m.agentViewVisible = false
			m.flash = "agent view has no agent work yet"
			break
		}
		m.agentViewVisible = true
		m.toolsScroll = 0
		m.agentViewIndex = clamp(m.agentViewIndex, 0, max(0, len(m.activeAgentViewItems())-1))
		m.flash = "agent view opened"
	case input == "/sessions":
		m.saveLastSession()
		if ok := m.refreshSessionsPicker(true); ok {
			m.sessionsVisible = true
			m.flash = "sessions opened"
		}
	case input == "/save":
		name, err := defaultChatSessionName()
		if err != nil {
			m.flash = fmt.Sprintf("save failed: %v", err)
			break
		}
		if err := m.saveSession(name); err != nil {
			m.flash = fmt.Sprintf("save failed: %v", err)
		} else {
			m.flash = fmt.Sprintf("session saved: %s", name)
		}
	case strings.HasPrefix(input, "/remember"):
		text := strings.TrimSpace(strings.TrimPrefix(input, "/remember"))
		switch {
		case text == "":
			m.flash = "usage: /remember <text>"
		case m.config.Remember == nil:
			m.flash = "memory not available"
		case m.config.Remember(text):
			m.flash = "remembered (pinned)"
		default:
			m.flash = "nothing to remember"
		}
	case strings.HasPrefix(input, "/save "):
		name := sanitizeChatSessionName(strings.TrimSpace(strings.TrimPrefix(input, "/save ")))
		if name == "" {
			m.flash = "save failed: missing session name"
			break
		}
		if err := m.saveSession(name); err != nil {
			m.flash = fmt.Sprintf("save failed: %v", err)
		} else {
			m.flash = fmt.Sprintf("session saved: %s", name)
		}
	case input == "/restore":
		name, err := latestChatSessionName()
		if err != nil {
			m.flash = fmt.Sprintf("restore failed: %v", err)
			break
		}
		if err := m.restoreSession(name); err != nil {
			m.flash = fmt.Sprintf("restore failed: %v", err)
		} else {
			m.flash = m.restoredFlash(name)
		}
	case strings.HasPrefix(input, "/restore "):
		name := sanitizeChatSessionName(strings.TrimSpace(strings.TrimPrefix(input, "/restore ")))
		if name == "" {
			m.flash = "restore failed: missing session name"
			break
		}
		if err := m.restoreSession(name); err != nil {
			m.flash = fmt.Sprintf("restore failed: %v", err)
		} else {
			m.flash = m.restoredFlash(name)
		}
	case input == "/tools" || input == "/toggle tools":
		m.setToolPanelsVisible(!m.toolPanelsVisible)
	case input == "/toggle tools on":
		m.setToolPanelsVisible(true)
	case input == "/toggle tools off":
		m.setToolPanelsVisible(false)
	case input == "/agents" || input == "/agents models":
		m.flash = fmt.Sprintf("unknown command: %s", input)
	case input == "/provider":
		m.openProviderPicker()
		m.flash = "providers opened"
	case input == "/models" || input == "/model":
		m.openModelPicker()
		m.flash = "models opened"
	case strings.HasPrefix(input, "/model "):
		arg := strings.TrimSpace(strings.TrimPrefix(input, "/model "))
		if arg != "" && m.config.SwitchModel != nil {
			models := m.modelsList
			if len(models) == 0 {
				models = append([]string(nil), m.config.AvailableModels...)
			}
			resolved := resolveModelName(models, arg)
			if resolved == "" {
				m.flash = fmt.Sprintf("unknown model %q — try /models", arg)
				break
			}
			newModel, err := m.config.SwitchModel(resolved)
			if err != nil {
				m.flash = fmt.Sprintf("error: %v", err)
			} else {
				m.model = newModel
				m.resetProviderDiagnostics()
				m.syncStatusData()
				m.flash = fmt.Sprintf("switched to %s", newModel)
			}
		}
	case input == "/skills":
		var sb strings.Builder
		sb.WriteString("Skills:\n")
		for _, s := range m.skills {
			marker := "○"
			if m.state != nil && m.state.SkillActivated(s.Name) {
				marker = "●"
			}
			sb.WriteString("  " + marker + " /" + s.Name + " — " + s.Description + "\n")
		}
		m.AddMessage(ChatMessage{Kind: MsgStatus, Content: sb.String()})
	case input == "/find":
		m.openSearchOverlay("")
		m.flash = "search opened"
	case strings.HasPrefix(input, "/find "):
		query := strings.TrimSpace(strings.TrimPrefix(input, "/find "))
		m.openSearchOverlay(query)
	case input == "/files":
		m.openWorkspaceFileBrowser("")
	case strings.HasPrefix(input, "/files "):
		query := strings.TrimSpace(strings.TrimPrefix(input, "/files "))
		m.openWorkspaceFileBrowser(query)
	case input == "/copy agent":
		if err := m.copyFn(plainCopyText(m.chatContent)); err != nil {
			m.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.flash = "agent copied"
		}
	case input == "/copy tools":
		if err := m.copyFn(plainCopyText(m.renderedToolsBuf())); err != nil {
			m.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.flash = "tools copied"
		}
	case input == "/copy code":
		if strings.TrimSpace(m.lastCodeBlock) == "" {
			m.flash = "copy failed: no code block yet"
		} else if err := m.copyFn(plainCopyText(m.lastCodeBlock)); err != nil {
			m.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.flash = "code copied"
		}
	case input == "/copy result":
		if strings.TrimSpace(m.lastToolResult) == "" {
			m.flash = "copy failed: no tool result yet"
		} else if err := m.copyFn(plainCopyText(m.lastToolResult)); err != nil {
			m.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.flash = "result copied"
		}
	case input == "/reload":
		if m.config.ReloadPlugins == nil {
			m.flash = "plugin reload not available in this session"
		} else {
			summary := m.config.ReloadPlugins()
			m.skills = skills.Load(m.config.WorkDir)
			m.flash = fmt.Sprintf("%s; %d skill(s) loaded", summary, len(m.skills))
		}
	default:
		m.flash = "unknown command: " + input
	}
	return m, nil
}

func (m ChatModel) helpTabs() []string {
	return []string{"Keys", "Chat Commands", "CLI Skills"}
}

// keyLabel translates Ctrl- prefix to macOS notation when running on darwin.
func keyLabel(s string) string {
	if runtime.GOOS != "darwin" {
		return s
	}
	// Replace both Ctrl- and Ctrl+ prefix forms
	s = strings.ReplaceAll(s, "Ctrl-", "⌃")
	s = strings.ReplaceAll(s, "Ctrl+", "⌃")
	return s
}

func (m ChatModel) helpLines() []string {
	switch m.helpTab {
	case 1:
		return applyKeyLabels([]string{
			"Chat commands",
			"",
			"Discovery and navigation:",
			"  /help              open this help overlay",
			"  /find              open search for current pane",
			"  /find <query>      search current pane with initial query",
			"  /files             browse workspace files and preview code",
			"  /files <query>     browse workspace files with an initial filter",
			"  /models            list available models",
			"  /model             list available models",
			"  /model <name>      switch to a model",
			"  /provider          open provider picker",
			"",
			"Session state:",
			"  /new               start a clean session",
			"  /make [prompt]     open pipeline config dialog",
			"                     pick writer/auditor models and start",
			"  /sessions          open saved sessions picker",
			"  /save [name]       save the current session",
			"  /restore [name]    restore a saved session",
			"  /stats             show latest turn and session token usage",
			"  /trace             open the debug trace overlay (requires forge -d)",
			"  /skills            list loaded skills",
			"",
			"Layout and display:",
			"  /theme             cycle chat themes",
			"  /theme <name>      select default, codex, opencode, low, light, dusk, midnight-ink, or eclipse",
			"",
			"Export and cleanup:",
			"  /copy agent        copy transcript",
			"  /copy tools        copy debug trace buffer",
			"  /copy code         copy latest code block",
			"  /copy result       copy latest tool result",
			"  /clear             clear conversation and debug trace buffer",
			"  /clear all         same as /clear",
			"  /clear agent       clear transcript",
			"  /clear tools       clear debug trace buffer",
			"  /exit              leave live mode",
			"  /quit              leave live mode",
			"  /reload            reload plugins, skills, and config",
		})
	case 2:
		return applyKeyLabels([]string{
			"CLI skills",
			"",
			"In chat:",
			"  /skills            list available skills and activation names",
			"  /<skill>           activate a loaded skill by name",
			"                     example: /skills, then /tdd",
			"",
			"Skill locations:",
			"  project: ./.forge/skills/",
			"  global:  ~/.config/forge/skills/",
			"",
			"CLI help:",
			"  forge help",
			"  forge help skills",
			"",
			"Inspect skills:",
			"  forge skills list",
			"  forge skills dir",
			"  forge skills status",
			"  forge skills search <query>",
			"  forge skills remove <name>",
			"",
			"Install/update skills:",
			"  forge skills install [--scope global|project] <source>",
			"  forge skills install [--scope global|project] --git <repo-url> [--subdir <path>]",
			"  forge skills install [--scope global|project] superpowers [skill-name ...]",
			"  forge skills update superpowers [--scope global|project]",
		})
	default:
		return applyKeyLabels([]string{
			"Keyboard shortcuts",
			"",
			"Pipeline/audit mode (Ctrl+P or /make):",
			"  /make <prompt>     start a pipeline session",
			"  Ctrl-P / v         toggle chat view / pipeline view",
			"  ← / →              focus writer / auditor pane",
			"  ↑ / ↓              scroll active pane",
			"  p                  toggle file preview pane",
			"  Space              advance turn (manual mode)",
			"  q / v              return to chat view",
			"",
			"Help navigation:",
			"  F1                 open help",
			"  ← / →              switch help tab",
			"  ↑ / ↓              scroll help",
			"  PgUp / PgDn        page help",
			"  Home / End         jump to top / bottom",
			"  Esc / Enter / ?    close help",
			"",
			"Prompt editing:",
			"  Enter              send current prompt",
			"  ← / →              move prompt cursor",
			"  Home / End         jump to start / end",
			"  Backspace          delete previous character",
			"",
			"Transcript navigation:",
			"  PgUp / PgDn        scroll conversation",
			"  Tab                cycle slash-command completion only",
			"  Mouse wheel        scroll conversation",
			"  Ctrl-F             open search for current pane",
			"  n / N              next / previous search hit",
			"  Ctrl-T / /tools    toggle tool activity panel",
			"",
			"Turn control:",
			"  Esc                cancel current run",
		})
	}
}

// applyKeyLabels runs every line through keyLabel for OS-aware key notation.
func applyKeyLabels(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = keyLabel(line)
	}
	return out
}

func scrollbarColumn(totalLines, visibleLines, scroll, height int) []string {
	if height <= 0 {
		return nil
	}
	col := make([]string, height)
	for i := range col {
		col[i] = " "
	}
	if totalLines <= visibleLines || visibleLines <= 0 {
		return col
	}
	thumbH := max(1, visibleLines*height/max(1, totalLines))
	if thumbH > height {
		thumbH = height
	}
	maxScroll := max(0, totalLines-visibleLines)
	thumbTop := 0
	if height > thumbH && maxScroll > 0 {
		thumbTop = (scroll * (height - thumbH)) / maxScroll
	}
	for i := 0; i < thumbH && thumbTop+i < height; i++ {
		col[thumbTop+i] = "█"
	}
	return col
}

func joinWithScrollbar(lines []string, scrollbar []string, width, height int) string {
	bodyStyle := lipgloss.NewStyle().Width(max(1, width)).Height(max(1, height))
	if len(lines) == 0 {
		lines = []string{""}
	}
	content := bodyStyle.Render(strings.Join(lines, "\n"))
	contentLines := strings.Split(content, "\n")
	if len(contentLines) < height {
		for len(contentLines) < height {
			contentLines = append(contentLines, strings.Repeat(" ", max(1, width)))
		}
	}
	joined := make([]string, 0, height)
	for i := 0; i < height; i++ {
		sb := "│"
		if i < len(scrollbar) {
			sb = scrollbar[i]
		}
		line := ""
		if i < len(contentLines) {
			line = contentLines[i]
		}
		joined = append(joined, line+sb)
	}
	return strings.Join(joined, "\n")
}

// saveLastSession persists the current conversation as "last-session" so it
// survives a quit. No-op when the transcript is empty, so a fresh session
// never clobbers the previous one.
func (m *ChatModel) saveLastSession() {
	if strings.TrimSpace(m.chatContent) == "" {
		return
	}
	_ = m.saveSession("last-session")
}

func (m *ChatModel) quitCmd() tea.Cmd {
	m.saveLastSession()
	return tea.Quit
}

func (m ChatModel) snapshot() chatSessionSnapshot {
	threadID := ""
	if m.config.CurrentThreadID != nil {
		threadID = m.config.CurrentThreadID()
	}
	return chatSessionSnapshot{
		SavedAt:      time.Now(),
		Model:        m.model,
		WorkDir:      m.workDir,
		ThreadID:     threadID,
		AgentBuf:     m.chatContent,
		ToolsBuf:     m.renderedToolsBuf(),
		InputBuf:     m.inputBuf,
		InputPos:     m.inputPos,
		ToolsVisible: boolPtr(m.toolsVisible),
		ContextFiles: append([]string(nil), m.contextFiles...),
		SessionUsage: m.sessionUsage,
	}
}

func (m *ChatModel) applySnapshot(s chatSessionSnapshot) {
	m.resetRecentActivity()
	m.restoreNote = ""
	if s.ThreadID != "" && m.config.RestoreHistory != nil {
		if n, err := m.config.RestoreHistory(s.ThreadID); err != nil {
			m.restoreNote = fmt.Sprintf("transcript restored; history replay failed: %v", err)
		} else if n > 0 {
			m.restoreNote = fmt.Sprintf("conversation restored (%d messages of history)", n)
		}
	}
	m.model = s.Model
	m.workDir = s.WorkDir
	m.chatContent = s.AgentBuf
	m.chatVisible = s.AgentBuf
	m.toolsSections = nil
	if s.ToolsBuf != "" {
		m.toolsSections = []toolsSection{{buf: s.ToolsBuf}}
	}
	m.inputBuf = s.InputBuf
	m.inputPos = s.InputPos
	m.toolsVisible = false
	m.contextFiles = append([]string(nil), s.ContextFiles...)
	m.sessionUsage = s.SessionUsage
	m.resetProviderDiagnostics()
	m.syncStatusData()
	m.turnAnchorMessageIndex = -1
	m.followMode = followBottom
	m.messages = nil
	if strings.TrimSpace(s.AgentBuf) != "" {
		m.messages = append(m.messages, ChatMessage{Kind: MsgStatus, Content: s.AgentBuf})
	}
	m.rebuildTranscriptStateFromMessages()
	m.chatViewport.SetContent(m.chatVisible)
}

func (m *ChatModel) saveSession(name string) error {
	path, err := chatSessionFile(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.snapshot(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (m *ChatModel) restoredFlash(name string) string {
	if m.restoreNote != "" {
		note := m.restoreNote
		m.restoreNote = ""
		return fmt.Sprintf("session restored: %s — %s", name, note)
	}
	return fmt.Sprintf("session restored: %s", name)
}

func (m *ChatModel) restoreSession(name string) error {
	path, err := chatSessionFile(name)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var s chatSessionSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	m.applySnapshot(s)
	return nil
}

func (m *ChatModel) setToolPanelsVisible(visible bool) {
	m.toolPanelsVisible = visible
	if visible {
		m.flash = "tool activity shown (Ctrl+T or /tools to hide)"
	} else {
		m.flash = "tool activity hidden (Ctrl+T or /tools to show)"
	}
	m.resizeChatViewport()
	m.viewportDirty = true
}

func (m ChatModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}
	theme := m.theme()
	headerData := m.statusSnapshot()
	header := renderStatusHeaderForHeight(theme, headerData, m.width, m.height)

	budget := m.normalChatLayoutBudget()

	chatBodyHeight := budget.Chat
	chatContentWidth := max(1, m.chatContentWidth())
	chatView := m.chatViewport.View()
	chatLines := strings.Split(chatView, "\n")
	chatTotalLines := len(strings.Split(m.chatVisible, "\n"))
	if strings.TrimSpace(m.chatVisible) == "" {
		empty := []string{
			"  Welcome to Forge.",
			"  Ready for your first request.",
		}
		chatLines = empty
		chatTotalLines = len(empty)
	}

	// Ensure chat lines always matches chatBodyHeight
	if len(chatLines) < chatBodyHeight {
		padding := make([]string, chatBodyHeight-len(chatLines))
		for i := range padding {
			padding[i] = ""
		}
		chatLines = append(chatLines, padding...)
	} else if len(chatLines) > chatBodyHeight {
		chatLines = chatLines[:chatBodyHeight]
	}

	chatScrollbar := scrollbarColumn(chatTotalLines, m.chatViewport.Height, m.chatViewport.YOffset, chatBodyHeight)
	chatBody := joinWithScrollbar(chatLines, chatScrollbar, chatContentWidth, chatBodyHeight)
	chatPane := lipgloss.NewStyle().
		Foreground(theme.Text).
		Width(m.chatPaneWidth()).
		Height(chatBodyHeight).
		Render(chatBody)
	if m.agentViewVisible {
		chatPane = m.renderAgentView(theme, chatBodyHeight)
	}
	debugDock := ""
	if m.debugSurfaceActive() {
		debugDock = m.renderTraceDock(theme)
	}
	liveRegion := m.renderLiveProgressSlot(theme)

	var inputBox string
	if m.pendingApproval != nil {
		inputBox = m.renderApprovalOverlay(theme)
	} else {
		inputBox = m.composer().Render(theme, m.width)
	}

	headerGap := lipgloss.NewStyle().Width(m.width).Render("")
	parts := []string{header, headerGap, chatPane}
	if debugDock != "" {
		parts = append(parts, debugDock)
	}
	if panel := m.renderAgentTaskPanel(theme); panel != "" {
		parts = append(parts, panel)
	}
	if cards := m.renderToolCardsPanel(theme); cards != "" {
		parts = append(parts, cards)
	}
	if changes := m.renderFileChangesPanel(theme); changes != "" {
		parts = append(parts, changes)
	}
	if preview := m.renderPendingInputPreview(theme); preview != "" {
		parts = append(parts, preview)
	}
	parts = append(parts, liveRegion, inputBox)
	if m.shouldShowNormalModeStatsFooter() {
		if statsLine := m.renderNormalModeStatsLine(theme); statsLine != "" {
			sep := lipgloss.NewStyle().
				Foreground(theme.Border).
				Render(strings.Repeat("─", m.width))
			parts = append(parts, sep)
			parts = append(parts, statsLine)
		}
	}
	base := lipgloss.NewStyle().
		Foreground(theme.Text).
		Width(m.width).
		Height(m.height).
		Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
	if m.helpVisible {
		return fillSurfaceRows(m.renderHelpOverlay(), m.width, theme.AppBG)
	}
	if m.statsVisible {
		return fillSurfaceRows(m.renderStatsOverlay(), m.width, theme.AppBG)
	}
	if m.traceVisible && m.debugEnabled {
		if overlay := m.renderTraceOverlay(); overlay != "" {
			return fillSurfaceRows(overlay, m.width, theme.AppBG)
		}
	}
	if m.searchVisible {
		return fillSurfaceRows(m.renderSearchOverlay(), m.width, theme.AppBG)
	}
	if m.filesVisible {
		return fillSurfaceRows(m.renderFilesOverlay(), m.width, theme.AppBG)
	}
	if m.modelsVisible {
		return fillSurfaceRows(m.renderModelsOverlay(), m.width, theme.AppBG)
	}
	if m.providersVisible {
		return fillSurfaceRows(m.renderProvidersOverlay(), m.width, theme.AppBG)
	}
	if m.sessionsVisible {
		return fillSurfaceRows(m.renderSessionsOverlay(), m.width, theme.AppBG)
	}
	return fillSurfaceRows(base, m.width, theme.AppBG)
}

func fillSurfaceRows(view string, width int, bg lipgloss.Color) string {
	width = max(1, width)
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		printable := ansiPrintableWidth(line)
		if printable < width {
			line += strings.Repeat(" ", width-printable)
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// approvalOverlayHeight estimates the height of the approval overlay.
func (m ChatModel) approvalOverlayHeight() int {
	action := m.pendingApproval
	if action == nil {
		return m.composer().Height(m.width)
	}

	// Base: outer border (2) + header (1) + tool info (1) + empty line (1) + prompt (1) = 6
	h := 6

	if action.Path != "" {
		h++ // path line
	}
	if action.Summary != "" && action.Summary != action.Detail {
		h++ // summary line (1 line for truncated)
	}

	detail := strings.TrimSpace(action.Detail)
	if detail != "" {
		detailLines := strings.Count(detail, "\n") + 1
		// Cap at 8 lines detail + 1 for the "... N more" indicator
		h += min(detailLines, 9)
		if detailLines > 8 {
			h++ // indicator line replaces one of the detail lines
		}
	}

	return h
}

// renderApprovalOverlay renders a rich approval dialog showing the diff content
// when a tool action requires user approval.
func (m ChatModel) renderApprovalOverlay(theme chatTheme) string {
	action := m.pendingApproval
	if action == nil {
		return ""
	}

	width := max(20, m.width-2)
	innerWidth := width - 4

	// Header
	header := lipgloss.NewStyle().
		Foreground(theme.Warning).
		Bold(true).
		Render("⚠ Approve Tool Call")

	// Tool info line
	toolInfo := lipgloss.NewStyle().
		Foreground(theme.AccentSecondary).
		Render(fmt.Sprintf("Tool: %s", action.Tool))

	// Path line if available
	var pathLine string
	if action.Path != "" {
		pathLine = lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Render(fmt.Sprintf("Path: %s", action.Path))
	}

	// Summary line if available and different from detail
	var summaryLine string
	if action.Summary != "" && action.Summary != action.Detail {
		summary := action.Summary
		if len(summary) > 80 {
			summary = summary[:80] + "…"
		}
		summaryLine = lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Render(summary)
	}

	// Detail content — render as diff if it looks like one, otherwise as plain text
	var detailContent string
	detail := strings.TrimSpace(action.Detail)
	if detail != "" {
		if looksLikeDiff(detail) {
			detailContent = enhancedDiffBlock(detail, innerWidth, theme)
		} else {
			// Render as indented code block
			codeStyle := lipgloss.NewStyle().
				Foreground(theme.Text).
				Width(innerWidth)
			detailContent = codeStyle.Render(detail)
		}
		// Cap detail display height so it doesn't take over the screen
		detailLines := strings.Split(detailContent, "\n")
		maxDetailLines := 8
		if len(detailLines) > maxDetailLines {
			detailLines = detailLines[:maxDetailLines]
			detailLines = append(detailLines, lipgloss.NewStyle().
				Foreground(theme.TextDim).
				Italic(true).
				Render(fmt.Sprintf("… %d more lines — see chat for full content", len(strings.Split(detail, "\n"))-maxDetailLines)))
			detailContent = strings.Join(detailLines, "\n")
		}
	}

	// Action prompt
	prompt := lipgloss.NewStyle().
		Foreground(theme.AccentPrimary).
		Bold(true).
		Render("[y] Approve  [n] Reject")

	// Build the body
	var bodyParts []string
	bodyParts = append(bodyParts, header)
	bodyParts = append(bodyParts, toolInfo)
	if pathLine != "" {
		bodyParts = append(bodyParts, pathLine)
	}
	if summaryLine != "" {
		bodyParts = append(bodyParts, summaryLine)
	}
	if detailContent != "" {
		bodyParts = append(bodyParts, "")
		bodyParts = append(bodyParts, detailContent)
	}
	bodyParts = append(bodyParts, "")
	bodyParts = append(bodyParts, prompt)

	body := strings.Join(bodyParts, "\n")

	// Wrap in a bordered overlay style
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Warning).
		Foreground(theme.Text).
		Width(width).
		Render(body)
}

// looksLikeDiff checks if text content appears to be a unified diff.
func looksLikeDiff(content string) bool {
	lines := strings.SplitN(content, "\n", 10)
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") ||
			strings.HasPrefix(line, "--- ") ||
			strings.HasPrefix(line, "+++ ") ||
			strings.HasPrefix(line, "@@ ") {
			return true
		}
	}
	return false
}

func (m ChatModel) renderLiveProgressSlot(theme chatTheme) string {
	gap := lipgloss.NewStyle().Width(m.width).Render("")
	message, busy := m.transientStatusMessage()
	if message == "" {
		return gap + "\n" + lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Width(m.width).
			Render("")
	}

	lines := strings.Split(message, "\n")
	rendered := make([]string, 0, len(lines)+1)
	rendered = append(rendered, gap)

	for i, line := range lines {
		prefix := "·"
		style := lipgloss.NewStyle().
			Width(m.width)

		if busy && i == len(lines)-1 {
			// Active entry gets spinner + accent color
			prefix = chatSpinnerGlyph(m.spinnerFrame)
			style = style.Foreground(theme.AccentPrimary).Bold(true)
		} else {
			style = style.Foreground(theme.TextDim)
		}

		rendered = append(rendered, style.Render(fitCell(prefix+" "+line, max(1, m.width))))
	}

	return strings.Join(rendered, "\n")
}

func (m ChatModel) renderPendingInputPreview(theme chatTheme) string {
	if !m.shouldShowPendingInputPreview() {
		return ""
	}
	lines := []string{"Queued input"}
	limit := min(3, len(m.pendingQueuedInput))
	for i := 0; i < limit; i++ {
		lines = append(lines, "  ↳ "+truncate(strings.TrimSpace(m.pendingQueuedInput[i]), max(20, m.width-8)))
	}
	if len(m.pendingQueuedInput) > limit {
		lines = append(lines, fmt.Sprintf("  … %d more", len(m.pendingQueuedInput)-limit))
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Border).
		Background(theme.AppBG).
		Foreground(theme.TextDim).
		Width(max(10, m.width-2)).
		Render(strings.Join(lines, "\n"))
}

func (m ChatModel) renderTraceDock(theme chatTheme) string {
	content := strings.TrimSpace(m.renderedToolsBuf())
	if stats := m.renderPerformanceTraceLine(); stats != "" {
		if content != "" {
			content += "\n"
		}
		content += stats
	}
	return renderTraceDockPanel(theme, content, m.config.DebugLogPath, m.width, m.debugDockHeight())
}

func (m ChatModel) renderPerformanceTraceLine() string {
	stats := m.lastRenderStats
	if stats.Rendered == 0 && stats.Hits == 0 && stats.Misses == 0 && stats.Lines == 0 {
		return ""
	}
	return fmt.Sprintf("rendered %d • cache hits %d • misses %d • lines %d", stats.Rendered, stats.Hits, stats.Misses, stats.Lines)
}

func (m ChatModel) renderNormalModeStatsLine(theme chatTheme) string {
	parts := make([]string, 0, 5)
	if m.statsDuration > 0 && m.statsUsage.OutputTokens > 0 {
		tokPerSec := float64(m.statsUsage.OutputTokens) / m.statsDuration.Seconds()
		parts = append(parts, fmt.Sprintf("%.0f tok/s", tokPerSec))
	} else if m.busy && !m.liveStatsStartedAt.IsZero() && m.liveStatsOutputChars > 0 {
		if elapsed := time.Since(m.liveStatsStartedAt); elapsed > 0 {
			approxOutputTokens := (m.liveStatsOutputChars + 3) / 4
			tokPerSec := float64(approxOutputTokens) / elapsed.Seconds()
			parts = append(parts, fmt.Sprintf("%.0f tok/s", tokPerSec))
		}
	}
	if m.statsUsage.InputTokens > 0 || m.statsUsage.OutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d in / %d out", m.statsUsage.InputTokens, m.statsUsage.OutputTokens))
	}
	if context := buildContextSummary(m.statusSnapshot()); context != "" {
		parts = append(parts, context)
	}
	if plan := m.currentPlanProgress(); plan != nil {
		if compact := renderPlanProgressBarCompact(*plan, theme); compact != "" {
			parts = append(parts, compact)
		}
	}
	if m.lastRenderStats.Hits > 0 || m.lastRenderStats.Misses > 0 {
		cachePart := fmt.Sprintf("cache %d hits", m.lastRenderStats.Hits)
		if m.lastRenderStats.Misses > 0 {
			cachePart += fmt.Sprintf(" / %d misses", m.lastRenderStats.Misses)
		}
		parts = append(parts, cachePart)
	}
	if len(parts) == 0 {
		return ""
	}
	line := strings.Join(parts, " • ")
	return lipgloss.NewStyle().
		Foreground(theme.TextDim).
		Width(m.width).
		Render(fitCell(line, max(1, m.width)))
}

func (m ChatModel) transientStatusMessage() (string, bool) {
	if len(m.liveProgress.Entries) > 0 {
		messages := make([]string, 0, len(m.liveProgress.Entries))
		for _, entry := range m.liveProgress.Entries {
			if normalized := normalizeStatusMessage(entry); normalized != "" {
				messages = append(messages, normalized)
			}
		}
		if len(messages) > 0 {
			// Cap visible entries at 3; oldest ones are dropped
			show := messages
			if len(show) > 3 {
				extra := len(show) - 3
				show = show[len(show)-3:]
				show[0] = fmt.Sprintf("… %d more", extra)
			}
			return strings.Join(show, "\n"), m.busy
		}
	}
	if m.busy {
		if status := normalizeStatusMessage(m.status); status != "" {
			return status, true
		}
		return "working", true
	}
	return normalizeStatusMessage(m.flash), false
}

func normalizeStatusMessage(message string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(message)), " ")
}

func normalizeProgressMessage(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if normalized, ok := normalizeRuntimeProgressMessage(content); ok {
		return normalized
	}
	return content
}

func (m *ChatModel) resetProgressCheckpointState() {
	m.lastProgressCheckpoint = ""
	m.lastProgressAt = time.Time{}
}

func (m ChatModel) shouldEmitProgressCheckpoint(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	if key != m.lastProgressCheckpoint {
		return true
	}
	// Suppress duplicate checkpoints for the same command/step signature while
	// allowing the same checkpoint to appear again in later turns.
	if m.lastProgressAt.IsZero() {
		return false
	}
	return time.Since(m.lastProgressAt) > 5*time.Minute
}

func (m *ChatModel) emitProgressCheckpoint(key, content string) {
	key = strings.TrimSpace(key)
	content = strings.TrimSpace(content)
	if key == "" || content == "" {
		return
	}
	if !m.shouldEmitProgressCheckpoint(key) {
		return
	}
	m.AddMessage(ChatMessage{
		Kind:    MsgCheckpoint,
		Content: content,
	})
	m.lastProgressCheckpoint = key
	m.lastProgressAt = time.Now()
}

func (m ChatModel) toolCallCheckpoint(ev llm.Event) (string, string) {
	agent := strings.TrimSpace(ev.Agent)
	summary := strings.TrimSpace(ev.Text)
	switch agent {
	case "read_file", "artifact_read":
		target := strings.Trim(strings.TrimSpace(summary), "\"'")
		if target == "" {
			return "", ""
		}
		label := target
		if !strings.HasPrefix(strings.ToLower(target), "docs/") {
			label = filepath.Base(target)
		}
		if strings.TrimSpace(label) == "" || label == "." {
			return "", ""
		}
		key := "tool:read:" + normalizeProgressComparable(label)
		return key, formatCheckpointNarrative(m.toolCallProgressLine(ev), agent, label)
	case "list_dir":
		target := strings.Trim(strings.TrimSpace(summary), "\"'")
		if target == "" || target == "." {
			target = "workspace root"
		}
		key := "tool:list:" + normalizeProgressComparable(target)
		return key, formatCheckpointNarrative(m.toolCallProgressLine(ev), agent, target)
	case "glob":
		pattern := strings.Trim(strings.TrimSpace(summary), "\"'")
		if pattern == "" {
			return "", ""
		}
		key := "tool:glob:" + normalizeProgressComparable(pattern)
		return key, formatCheckpointNarrative(m.toolCallProgressLine(ev), agent, fmt.Sprintf("%q", pattern))
	case "search":
		pattern := strings.Trim(strings.TrimSpace(summary), "\"'")
		if pattern == "" {
			return "", ""
		}
		key := "tool:search:" + normalizeProgressComparable(pattern)
		return key, formatCheckpointNarrative(m.toolCallProgressLine(ev), agent, fmt.Sprintf("%q", pattern))
	case "code_search", "git_status", "git_log", "git_diff", "git_branch_state", "git_merge_status", "tool_help":
		if summary == "" {
			return "", ""
		}
		key := "tool:" + agent + ":" + normalizeProgressComparable(summary)
		return key, formatCheckpointNarrative(m.toolCallProgressLine(ev), agent, summary)
	case "lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols":
		if summary == "" {
			return "", ""
		}
		key := "tool:" + agent + ":" + normalizeProgressComparable(summary)
		return key, formatCheckpointNarrative(m.toolCallProgressLine(ev), agent, summary)
	default:
		return "", ""
	}
}

func (m ChatModel) shouldPersistToolCallCheckpoint(ev llm.Event) bool {
	switch strings.TrimSpace(ev.Agent) {
	case "read_file", "artifact_read", "list_dir", "glob", "search", "code_search":
		return false
	default:
		return true
	}
}

func (m ChatModel) toolResultCheckpoint(ev llm.Event) (string, string) {
	agent := strings.TrimSpace(ev.Agent)
	if agent == "" || agent == "runtime" || agent == "delegate" {
		return "", ""
	}
	command := checkpointCommandLabel(agent, strings.TrimSpace(m.lastToolSummary[agent]))
	if command == "" {
		return "", ""
	}
	output := strings.TrimSpace(ev.Text)
	if output == "" {
		output = strings.TrimSpace(ev.Content)
	}
	lines := checkpointOutputLines(output, ev.IsError)
	checkpoint := formatCheckpointRunMessage(command, lines)
	key := "run:" + normalizeProgressComparable(command)
	if len(lines) > 0 {
		key += "|" + normalizeProgressComparable(lines[0])
	}
	return key, checkpoint
}

func checkpointCommandLabel(agent, summary string) string {
	agent = strings.TrimSpace(agent)
	summary = strings.TrimSpace(summary)
	switch agent {
	case "run_command":
		if summary == "" {
			return "command"
		}
		return summary
	case "git_status":
		return "git status --porcelain"
	case "git_log":
		if summary == "" {
			return "git log --oneline"
		}
		return "git log --oneline " + summary
	case "git_diff":
		if summary == "" {
			return "git diff HEAD"
		}
		return "git diff " + summary
	default:
		return ""
	}
}

func checkpointOutputLines(output string, isError bool) []string {
	output = strings.TrimSpace(output)
	if output == "" {
		if isError {
			return []string{"tool reported an error"}
		}
		return nil
	}
	raw := strings.Split(output, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, truncate(line, 140))
	}
	if len(lines) == 0 {
		if isError {
			return []string{"tool reported an error"}
		}
		return nil
	}
	if len(lines) > 1 && strings.EqualFold(lines[len(lines)-1], "exit 0") {
		lines = lines[:len(lines)-1]
	}
	const maxLines = 4
	if len(lines) > maxLines {
		remaining := len(lines) - maxLines
		lines = append(lines[:maxLines], fmt.Sprintf("… +%d lines", remaining))
	}
	return lines
}

func formatCheckpointRunMessage(command string, lines []string) string {
	// Collapse multiline/whitespace-heavy commands to one bounded line.
	command = truncate(strings.Join(strings.Fields(command), " "), 100)
	if command == "" {
		command = "command"
	}
	if len(lines) == 0 {
		return "• Ran " + command
	}
	var b strings.Builder
	b.WriteString("• Ran ")
	b.WriteString(command)
	b.WriteString("\n  └ ")
	b.WriteString(lines[0])
	for _, line := range lines[1:] {
		b.WriteString("\n    ")
		b.WriteString(line)
	}
	return b.String()
}

func formatCheckpointToolMessage(toolName, detail string) string {
	toolName = strings.TrimSpace(toolName)
	detail = strings.TrimSpace(detail)
	if toolName == "" {
		return ""
	}
	if detail == "" {
		return "• " + toolName
	}
	return "• " + toolName + "\n  └ " + detail
}

func formatCheckpointNarrative(narrative, toolName, detail string) string {
	narrative = strings.TrimSpace(narrative)
	if narrative != "" {
		return "• " + narrative
	}
	return formatCheckpointToolMessage(toolName, detail)
}

func combineProgressNarrative(current, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if next == "" {
		return current
	}
	if current == "" || current == next {
		return next
	}
	if equivalentProgressLine(current, next) {
		return preferEquivalentProgressLine(current, next)
	}
	if isGenericProgressLine(next) {
		return current
	}
	if isGenericProgressLine(current) {
		return next
	}
	if strings.EqualFold(current, next) {
		return current
	}
	return next
}

func equivalentProgressLine(current, next string) bool {
	currentSig := progressSignature(current)
	nextSig := progressSignature(next)
	if currentSig != "" && currentSig == nextSig {
		return true
	}
	return false
}

func preferEquivalentProgressLine(current, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if current == "" {
		return next
	}
	if next == "" {
		return current
	}

	currentPenalty := progressNoisePenalty(current)
	nextPenalty := progressNoisePenalty(next)
	if currentPenalty < nextPenalty {
		return current
	}
	if nextPenalty < currentPenalty {
		return next
	}
	if len(next) < len(current) {
		return next
	}
	return current
}

func progressNoisePenalty(content string) int {
	lower := strings.ToLower(content)
	penalty := 0
	if strings.Contains(lower, "[approval needed]") {
		penalty += 3
	}
	if strings.Contains(lower, " for context") {
		penalty++
	}
	if strings.Contains(lower, " that match ") {
		penalty++
	}
	if strings.ContainsAny(content, "\"'") {
		penalty++
	}
	return penalty
}

func progressSignature(content string) string {
	normalized := normalizeProgressComparable(content)
	if normalized == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(normalized, "reading "):
		target := strings.TrimSpace(strings.TrimPrefix(normalized, "reading "))
		target = strings.TrimSuffix(target, " for context")
		return "read:" + progressResourceToken(target)
	case strings.HasPrefix(normalized, "finding files matching "):
		pattern := strings.TrimSpace(strings.TrimPrefix(normalized, "finding files matching "))
		return "glob:" + progressPatternToken(pattern)
	case strings.HasPrefix(normalized, "finding files that match "):
		pattern := strings.TrimSpace(strings.TrimPrefix(normalized, "finding files that match "))
		return "glob:" + progressPatternToken(pattern)
	case strings.HasPrefix(normalized, "searching for "):
		pattern := strings.TrimSpace(strings.TrimPrefix(normalized, "searching for "))
		return "search:" + progressPatternToken(pattern)
	case strings.HasPrefix(normalized, "running "):
		cmd := strings.TrimSpace(strings.TrimPrefix(normalized, "running "))
		cmd = strings.TrimPrefix(cmd, "[approval needed] ")
		return "run:" + strings.Join(strings.Fields(cmd), " ")
	default:
		return ""
	}
}

func progressPatternToken(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	pattern = strings.Trim(pattern, "\"'")
	return pattern
}

func progressResourceToken(resource string) string {
	resource = strings.TrimSpace(resource)
	resource = strings.Trim(resource, "\"'")
	resource = strings.TrimRight(resource, ".,:;")
	if resource == "" {
		return ""
	}
	if strings.Contains(resource, "/") {
		resource = filepath.Base(resource)
	}
	return strings.ToLower(resource)
}

func normalizeProgressComparable(content string) string {
	content = strings.ToLower(strings.TrimSpace(content))
	content = strings.Join(strings.Fields(content), " ")
	if content == "" {
		return ""
	}
	return content
}

func (m ChatModel) progressEventLine(ev llm.Event) string {
	line := normalizeProgressMessage(ev.Text)
	if line == "" {
		return ""
	}
	if !isGenericProgressLine(line) {
		return line
	}
	if !m.hasLiveWorkingMessage() {
		return line
	}
	current := strings.TrimSpace(m.messages[m.recentActivityIndex].Content)
	if current == "" {
		return line
	}
	if !isGenericProgressLine(current) {
		return ""
	}
	if strings.EqualFold(current, line) {
		return ""
	}
	return ""
}

func isGenericProgressLine(content string) bool {
	lower := strings.ToLower(strings.TrimSpace(content))
	switch {
	case strings.Contains(lower, "getting the lay of the land"):
		return true
	case strings.HasPrefix(lower, "starting analysis pass"):
		return true
	case strings.Contains(lower, "surveying the repository"):
		return true
	case strings.Contains(lower, "reviewing the repository structure"):
		return true
	case strings.Contains(lower, "connecting findings"):
		return true
	case strings.Contains(lower, "cross-checking gathered evidence"):
		return true
	case strings.Contains(lower, "cross-checking the repository scan results"):
		return true
	case strings.Contains(lower, "cross-checking") && strings.Contains(lower, "for consistency"):
		return true
	case strings.Contains(lower, "synthesizing findings"):
		return true
	case strings.Contains(lower, "turning ") && strings.Contains(lower, " into concrete recommendations"):
		return true
	case strings.Contains(lower, "drafting the response"):
		return true
	case strings.Contains(lower, "refining the response"):
		return true
	case strings.Contains(lower, "still working on your request"):
		return true
	case strings.Contains(lower, "continuing to process your request"):
		return true
	case strings.Contains(lower, "no action needed yet"):
		return true
	case strings.Contains(lower, "still analyzing repository details"):
		return true
	case strings.Contains(lower, "model is planning the first execution step"):
		return true
	case strings.Contains(lower, "model is planning the next execution step"):
		return true
	case strings.Contains(lower, "still waiting for model output on this step"):
		return true
	case strings.Contains(lower, "model is still reasoning through this step"):
		return true
	case strings.Contains(lower, "longer than usual, still waiting on model output"):
		return true
	case strings.Contains(lower, "reviewing the model response"):
		return true
	default:
		return false
	}
}

func normalizeRuntimeProgressMessage(content string) (string, bool) {
	raw := strings.TrimSpace(content)
	lower := strings.ToLower(raw)
	const compactPrefix = "react runtime: compacted context "
	if strings.HasPrefix(lower, compactPrefix) {
		return "Compacted context " + strings.TrimSpace(raw[len(compactPrefix):]), true
	}
	if strings.HasPrefix(lower, "react runtime: executing turn ") {
		turnText := strings.TrimSpace(strings.TrimPrefix(lower, "react runtime: executing turn "))
		return fmt.Sprintf("Starting analysis pass %s", turnText), true
	}
	if strings.Contains(lower, "cancelled") {
		return "I stopped this run on request", true
	}
	return "", false
}

func isCompactionProgressLine(content string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(content)), "compacted context ")
}

func (m ChatModel) toolResultProgressLine(ev llm.Event) string {
	agent := strings.TrimSpace(ev.Agent)
	if agent == "" || agent == "runtime" || agent == "delegate" {
		return ""
	}
	label := displayAgentLabel(agent)
	if ev.IsError {
		reason := compactStatusText(ev.Text)
		if reason == "" {
			reason = "tool error"
		}
		return fmt.Sprintf("%s hit an issue: %s", label, reason)
	}
	// Non-error tool completions are usually noise in the main pane because
	// corresponding tool-call progress already appeared.
	return ""
}

func (m ChatModel) toolCallProgressLine(ev llm.Event) string {
	agent := strings.TrimSpace(ev.Agent)
	if agent == "" {
		return ""
	}
	summary := compactStatusText(ev.Text)
	switch agent {
	case "read_file", "artifact_read":
		if summary == "" {
			return "Reading a file for context"
		}
		return fmt.Sprintf("Reading %s", summary)
	case "list_dir":
		if summary == "" || summary == "." {
			return "Scanning the workspace layout"
		}
		return fmt.Sprintf("Scanning %s", summary)
	case "search":
		if summary == "" {
			return "Searching the repository"
		}
		return fmt.Sprintf("Searching for %s", summary)
	case "glob":
		if summary == "" {
			return "Finding relevant files"
		}
		return fmt.Sprintf("Finding files matching %s", summary)
	case "code_search":
		if summary == "" {
			return "Searching code for relevant references"
		}
		return fmt.Sprintf("Searching code in %s", summary)
	case "git_status":
		return "Checking current git status"
	case "git_log":
		return "Reviewing recent commits"
	case "git_diff":
		return "Reviewing current diff"
	case "run_command":
		if summary == "" {
			return "Running a command"
		}
		return fmt.Sprintf("Running %s", summary)
	case "exec_session_start":
		if summary == "" {
			return "Starting terminal session"
		}
		return fmt.Sprintf("Starting terminal session: %s", summary)
	case "exec_session_resize":
		return "Resizing terminal session"
	case "exec_session_write":
		return "Writing to terminal session"
	case "exec_session_stop":
		return "Stopping terminal session"
	default:
		if summary == "" {
			return ""
		}
		return fmt.Sprintf("%s: %s", agent, summary)
	}
}
