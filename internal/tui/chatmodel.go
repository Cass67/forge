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
	"sort"
	"strings"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/auth"
	"forge/internal/chatgptauth"
	"forge/internal/chatstate"
	"forge/internal/claudeauth"
	"forge/internal/codexusage"
	"forge/internal/copilot"
	"forge/internal/llm"
	"forge/internal/secscan"
	"forge/internal/skills"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"
)

type chatTickMsg time.Time
type chatApprovalMsg tools.Action

// agentNudgeMsg carries a nudge update from the runtime to the TUI.
type agentNudgeMsg NudgeSuggestion
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
	autoSkillsMode         string
	state                  *chatstate.State
	currentNudge           NudgeSuggestion
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
		autoSkillsMode:         cfg.AutoSkillsMode,
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

func (m ChatModel) delegateResultLabel() string {
	if state := m.delegateResultState(); state.role != "" {
		return displayAgentLabel(state.role)
	}
	return "Agent"
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
		return strings.Count(m.pendingApproval.Summary, "\n") + 6
	}
	return m.composer().Height(m.width)
}

func (m ChatModel) normalModeStatsFooterHeight() int {
	if !m.shouldShowNormalModeStatsFooter() {
		return 0
	}
	return 2
}

func (m ChatModel) liveStatusSlotHeight() int {
	return 1
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
		Pending:     m.pendingInputPreviewHeight(),
		LiveStatus:  m.liveStatusSlotHeight(),
		Input:       m.inputHeight(),
		StatsFooter: m.normalModeStatsFooterHeight(),
	}
	b.Chat = max(1, m.height-b.Header-b.HeaderGap-b.DebugDock-b.Pending-b.LiveStatus-b.TaskPanel-b.Input-b.StatsFooter)
	b.Total = b.Header + b.HeaderGap + b.Chat + b.DebugDock + b.Pending + b.LiveStatus + b.TaskPanel + b.Input + b.StatsFooter
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

func (m ChatModel) toolsPaneWidth() int {
	if !m.hasAgentWorkPane() || m.width < 90 {
		return 0
	}
	return clamp(m.width/3, 34, max(34, m.width-40))
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

func (m ChatModel) renderedToolsBuf() string {
	var sb strings.Builder
	for _, sec := range m.toolsSections {
		if sec.collapsed && sec.summary != "" {
			sb.WriteString(sec.summary)
			sb.WriteByte('\n')
		} else {
			sb.WriteString(sec.buf)
		}
	}
	return sb.String()
}

func (m ChatModel) renderedAgentWorkBuf() string {
	var sb strings.Builder
	if stateBuf := strings.TrimSpace(m.renderedAgentTaskStateBuf(time.Now())); stateBuf != "" {
		sb.WriteString(stateBuf)
		sb.WriteString("\n\n")
	}
	for _, sec := range m.toolsSections {
		if strings.TrimSpace(sec.role) == "" {
			continue
		}
		if sec.collapsed && sec.summary != "" {
			sb.WriteString(sec.summary)
			sb.WriteByte('\n')
		} else {
			sb.WriteString(sec.buf)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m ChatModel) activeAgentViewItems() []chatAgentTaskState {
	items := make([]chatAgentTaskState, 0, len(m.agentTasks))
	for _, task := range m.agentTasks {
		if strings.TrimSpace(task.ID) == "" || isTerminalAgentTaskStatus(task.Status) {
			continue
		}
		items = append(items, task)
	}
	return items
}

func (m ChatModel) selectedAgentViewTask() (chatAgentTaskState, bool) {
	items := m.activeAgentViewItems()
	if len(items) == 0 {
		return chatAgentTaskState{}, false
	}
	idx := clamp(m.agentViewIndex, 0, len(items)-1)
	return items[idx], true
}

func (m ChatModel) renderedAgentViewBuf() string {
	if task, ok := m.selectedAgentViewTask(); ok {
		role := strings.TrimSpace(task.Role)
		var sb strings.Builder
		if line := formatChatAgentTaskLine(task, time.Now()); line != "" {
			sb.WriteString("Agent task\n")
			sb.WriteString(line)
			sb.WriteString("\n\n")
		}
		for _, sec := range m.toolsSections {
			if role == "" || strings.TrimSpace(sec.role) != role {
				continue
			}
			if sec.collapsed && sec.summary != "" {
				sb.WriteString(sec.summary)
				sb.WriteByte('\n')
			} else {
				sb.WriteString(sec.buf)
			}
		}
		return strings.TrimRight(sb.String(), "\n")
	}
	return m.renderedAgentWorkBuf()
}

func (m ChatModel) renderAgentView(theme chatTheme, height int) string {
	width := max(1, m.width)
	visible := m.agentViewVisibleLineCount(height)
	contentWidth := max(1, width-2)
	lines := m.agentViewWrappedLines(contentWidth)
	offset := clamp(m.toolsScroll, 0, m.agentViewMaxScrollForHeight(height))
	end := min(len(lines), offset+visible)
	bodyLines := append([]string(nil), lines[offset:end]...)
	for len(bodyLines) < visible {
		bodyLines = append(bodyLines, "")
	}
	scrollbar := scrollbarColumn(len(lines), visible, offset, visible)
	body := joinWithScrollbar(bodyLines, scrollbar, contentWidth, visible)
	title := " Agent view "
	if task, ok := m.selectedAgentViewTask(); ok {
		items := m.activeAgentViewItems()
		title = fmt.Sprintf(" Agent view %d/%d: %s ", clamp(m.agentViewIndex, 0, max(0, len(items)-1))+1, len(items), strings.TrimSpace(task.ID))
	}
	footer := lipgloss.NewStyle().Foreground(theme.TextDim).Width(width).Render("Tab cycle agents • Esc close")
	return lipgloss.NewStyle().Foreground(theme.Text).Width(width).Height(height).Render(lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true).Width(width).Render(fitCell(title, width)),
		body,
		footer,
	))
}

func (m ChatModel) agentViewVisibleLineCount(height int) int {
	return max(1, height-2)
}

func (m ChatModel) agentViewWrappedLines(contentWidth int) []string {
	lines := strings.Split(lipgloss.NewStyle().Width(max(1, contentWidth)).Render(strings.TrimSpace(m.renderedAgentViewBuf())), "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		return []string{"No agent work yet."}
	}
	return lines
}

func (m ChatModel) agentViewMaxScroll() int {
	return m.agentViewMaxScrollForHeight(m.chatViewport.Height)
}

func (m ChatModel) agentViewMaxScrollForHeight(height int) int {
	contentWidth := max(1, m.width-2)
	lines := m.agentViewWrappedLines(contentWidth)
	return max(0, len(lines)-m.agentViewVisibleLineCount(height))
}

func (m *ChatModel) cycleAgentView(delta int) {
	items := m.activeAgentViewItems()
	if len(items) == 0 {
		m.agentViewIndex = 0
		return
	}
	m.agentViewIndex = (m.agentViewIndex + delta) % len(items)
	if m.agentViewIndex < 0 {
		m.agentViewIndex += len(items)
	}
	m.toolsScroll = 0
}

func (m ChatModel) renderedAgentTaskStateBuf(now time.Time) string {
	if len(m.agentTasks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Agent tasks\n")
	for _, task := range m.agentTasks {
		if strings.TrimSpace(task.ID) == "" {
			continue
		}
		line := formatChatAgentTaskLine(task, now)
		if strings.TrimSpace(line) != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func formatChatAgentTaskLine(task chatAgentTaskState, now time.Time) string {
	id := strings.TrimSpace(task.ID)
	if id == "" {
		return ""
	}
	role := strings.TrimSpace(task.Role)
	if role == "" {
		role = "unknown-role"
	}
	status := strings.TrimSpace(task.Status)
	if status == "" {
		status = "running"
	}
	parts := []string{fmt.Sprintf("- %s (%s): %s", id, role, status)}
	if at := agentTaskDisplayTime(task); !at.IsZero() {
		parts = append(parts, "last "+at.Format("15:04:05"))
	}
	if elapsed := agentTaskElapsed(task, now); elapsed != "" {
		parts = append(parts, "elapsed "+elapsed)
	}
	if tool := strings.TrimSpace(task.LastToolName); tool != "" {
		activity := tool
		if len(task.RecentActivity) > 0 {
			last := task.RecentActivity[len(task.RecentActivity)-1]
			if summary := strings.TrimSpace(last.Summary); summary != "" {
				activity += " " + redactChatAgentTaskText(summary)
			}
		}
		parts = append(parts, activity)
	}
	if isTerminalAgentTaskStatus(status) {
		if result := strings.TrimSpace(task.Result); result != "" {
			parts = append(parts, "result: "+truncate(redactChatAgentTaskText(result), 120))
		}
		if errText := strings.TrimSpace(task.Error); errText != "" {
			parts = append(parts, "error: "+truncate(redactChatAgentTaskText(errText), 120))
		}
	}
	return strings.Join(parts, "; ")
}

func redactChatAgentTaskText(text string) string {
	if text == "" {
		return ""
	}
	scanner := secscan.NewDefaultScanner()
	return secscan.Redact(text, scanner.Scan(text))
}

func agentTaskDisplayTime(task chatAgentTaskState) time.Time {
	for _, candidate := range []time.Time{task.LastActivityAt, task.CompletedAt, task.StartedAt, task.CreatedAt} {
		if !candidate.IsZero() {
			return candidate
		}
	}
	return time.Time{}
}

func agentTaskElapsed(task chatAgentTaskState, now time.Time) string {
	start := task.StartedAt
	if start.IsZero() {
		start = task.CreatedAt
	}
	if start.IsZero() {
		return ""
	}
	end := task.CompletedAt
	if end.IsZero() {
		end = task.LastActivityAt
	}
	if end.IsZero() {
		end = now
	}
	if end.Before(start) {
		return ""
	}
	return end.Sub(start).Round(time.Second).String()
}

func isTerminalAgentTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "killed", "not_found":
		return true
	default:
		return false
	}
}

func (m *ChatModel) clearToolsSections() {
	m.toolsSections = nil
}

func (m ChatModel) toolsWrappedLines() []string {
	rendered := m.renderedAgentWorkBuf()
	if strings.TrimSpace(rendered) == "" {
		return nil
	}
	toolsWidth := m.width - m.chatPaneWidth()
	toolsInnerWidth := max(1, toolsWidth-2)
	toolsContentWidth := max(1, toolsInnerWidth-1)
	wrappedTools := lipgloss.NewStyle().Width(toolsContentWidth).Render(rendered)
	return strings.Split(wrappedTools, "\n")
}

func (m ChatModel) renderToolsPane(theme chatTheme, height int) string {
	width := m.toolsPaneWidth()
	if width <= 0 {
		return ""
	}
	lines := m.toolsWrappedLines()
	if len(lines) == 0 {
		return ""
	}
	visible := max(1, height-2)
	offset := clamp(m.toolsScroll, 0, max(0, len(lines)-visible))
	end := min(len(lines), offset+visible)
	bodyLines := append([]string(nil), lines[offset:end]...)
	for len(bodyLines) < visible {
		bodyLines = append(bodyLines, "")
	}
	contentWidth := max(1, width-3)
	scrollbar := scrollbarColumn(len(lines), visible, offset, visible)
	body := joinWithScrollbar(bodyLines, scrollbar, contentWidth, visible)
	title := " Agent work "
	if role := strings.TrimSpace(m.activeSubAgent); role != "" {
		title = " " + displayAgentLabel(role) + " "
	}
	return lipgloss.NewStyle().
		Foreground(theme.Text).
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(theme.Border).
		Width(width).
		Height(height).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true).Width(max(1, width-1)).Render(fitCell(title, max(1, width-1))),
			body,
		))
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

	case agentNudgeMsg:
		nudge := NudgeSuggestion(msg)
		m.currentNudge = nudge
		m.statusData.AgentMode = nudge.Label
		if nudge.Flash != "" && m.flash == "" {
			m.flash = nudge.Flash
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

func (m ChatModel) handleLLMEvent(ev llm.Event) (tea.Model, tea.Cmd) {
	// Sub-agent events primarily render in the tools pane, with human-readable
	// prose mirrored into the main transcript.
	if ev.SubAgent != "" {
		return m.handleSubAgentEvent(ev)
	}

	switch ev.Kind {
	case llm.EventAgentTask:
		return m.handleAgentTaskEvent(ev)
	case llm.EventToken:
		if token := sanitizeAssistantTokenForDisplay(ev.Text); token != "" {
			if m.liveStatsStartedAt.IsZero() {
				m.liveStatsStartedAt = time.Now()
			}
			m.liveStatsOutputChars += len(token)
			m.AppendToLastAgentLabeled(token, ev.Agent)
		}
	case llm.EventRetry:
		m.discardPendingAssistantMessage()
		if msg := strings.TrimSpace(ev.Text); msg != "" {
			m.AddWorkingMessage(msg)
		}
	case llm.EventToolCall:
		assistantLive := m.hasLiveAssistantMessage()
		if ev.Agent != "runtime" {
			m.markLastAssistantRecordFinal()
		}
		if ev.Agent == "runtime" {
			m.AddWorkingMessage(ev.Text)
			return m, nil
		}
		if strings.TrimSpace(ev.Agent) != "" {
			m.lastToolSummary[strings.TrimSpace(ev.Agent)] = strings.TrimSpace(ev.Text)
		}
		if !assistantLive {
			if m.shouldPersistToolCallCheckpoint(ev) {
				if key, checkpoint := m.toolCallCheckpoint(ev); checkpoint != "" {
					m.emitProgressCheckpoint(key, checkpoint)
				}
			}
			if line := m.toolCallProgressLine(ev); line != "" {
				m.UpdateRecentActivity("", line)
			}
		}
		if !m.debugEnabled {
			return m, nil
		}
		sec := m.currentToolsSection("")
		if sec.buf != "" && !strings.HasSuffix(sec.buf, "\n\n") {
			sec.buf += "\n"
		}
		m.appendTools("", "────────────────────────\n")
		m.appendTools("", fmt.Sprintf("● %s\n", ev.Agent))
		m.appendTools("", fmt.Sprintf("  %s\n", ev.Text))
	case llm.EventToolResult:
		if ev.Agent == "__task_context" {
			if !ev.IsError {
				m.upsertTaskContextMessage(ev.Text)
			}
			return m, nil
		}
		if ev.Agent == "__validation" {
			if text := strings.TrimSpace(ev.Text); text != "" {
				m.AddMessage(ChatMessage{Kind: MsgStatus, Content: text})
			}
			return m, nil
		}
		if ev.Agent == "spawn_agent" && !ev.IsError {
			var spawnResult struct {
				Role string `json:"role"`
			}
			role := ""
			if json.Unmarshal([]byte(ev.Text), &spawnResult) == nil && spawnResult.Role != "" {
				role = spawnResult.Role
			} else if json.Unmarshal([]byte(ev.Content), &spawnResult) == nil && spawnResult.Role != "" {
				role = spawnResult.Role
			}
			if role != "" {
				m.AddMessage(ChatMessage{
					Kind:    MsgStatus,
					Content: fmt.Sprintf("delegating to %s", displayAgentLabel(role)),
				})
			}
			return m, nil
		}
		if ev.Agent == "update_plan" && !ev.IsError {
			m.upsertPlanMessage(ev.Text)
		}
		if ev.Content != "" {
			m.lastToolResult = ev.Content
		} else if ev.Text != "" {
			m.lastToolResult = ev.Text
		}
		if key, checkpoint := m.toolResultCheckpoint(ev); checkpoint != "" && !m.hasLiveAssistantMessage() {
			m.emitProgressCheckpoint(key, checkpoint)
		}
		if m.debugEnabled {
			if line := m.toolResultProgressLine(ev); line != "" {
				m.UpdateRecentActivity("", line)
			}
		}
		if status := runtimeStatusMessage(ev); status != "" {
			m.AddMessage(ChatMessage{Kind: MsgStatus, Content: status})
			m.flash = status
		}
		if ev.Agent == "delegate" {
			state := m.delegateResultState()
			label := "Agent"
			if state.role != "" {
				label = displayAgentLabel(state.role)
			}
			result := ""
			if !ev.IsError && !state.transcriptVisible {
				result = selectDelegateTranscript(ev.Text, ev.Content)
			}
			m.clearWorkingMessage()
			if !ev.IsError {
				if result := strings.TrimSpace(result); result != "" {
					stamp := time.Now().Format("15:04:05")
					m.AddMessage(ChatMessage{
						Kind:    MsgAgent,
						Header:  label + " • " + stamp,
						Content: result,
					})
				}
			} else if status := compactStatusText(ev.Text); status != "" {
				m.AddMessage(ChatMessage{
					Kind:    MsgStatus,
					Content: "status: " + status,
				})
			}
			m.pendingSubAgentSummary = nil
		}
		if !m.debugEnabled {
			if ev.IsError && !isRecoverableToolFeedback(ev) {
				m.AddMessage(ChatMessage{
					Kind:    MsgStatus,
					Content: "Error: " + compactStatusText(ev.Text),
				})
			}
			return m, nil
		}
		if ev.IsError {
			m.appendTools("", fmt.Sprintf("  status: ✗ %s\n", ev.Text))
		} else if ev.Content != "" {
			truncated := truncate(ev.Content, 200)
			m.appendTools("", fmt.Sprintf("  status: ✓\n  %s\n", truncated))
		} else {
			m.appendTools("", fmt.Sprintf("  status: ✓ %s\n", truncate(ev.Text, 200)))
		}
	case llm.EventRoundStart:
		m.appendTools("", fmt.Sprintf("\n── round %d ──\n", ev.Round))
	case llm.EventDone:
		m.busy = false
		m.activeSubAgent = ""
		m.pendingQueuedInput = nil
		m.finalizeLiveProgressRecord()
		m.markLastAssistantRecordFinal()
		m.resetProgressCheckpointState()
		m.status = "ready"
		m.syncStatusData()
		if len(m.toolsSections) > 0 {
			m.appendTools("", "status: complete\n")
		}
	case llm.EventError:
		m.busy = false
		m.pendingQueuedInput = nil
		m.finalizeLiveProgressRecord()
		m.markLastAssistantRecordFinal()
		m.resetProgressCheckpointState()
		m.status = "error"
		m.syncStatusData()
		errMsg := eventErrorMessage(ev)
		m.appendTools("", fmt.Sprintf("  ✗ %s\n", errMsg))
		m.flash = "error: " + errMsg
		m.AddMessage(ChatMessage{
			Kind:    MsgStatus,
			Content: "Error: " + errMsg,
		})
	case llm.EventAbort:
		m.busy = false
		m.activeSubAgent = ""
		m.pendingQueuedInput = nil
		m.finalizeLiveProgressRecord()
		m.markLastAssistantRecordFinal()
		m.resetProgressCheckpointState()
		m.status = "ready"
		m.syncStatusData()
	case llm.EventStats:
		m.statsDuration = ev.Duration
		m.statsUsage = ev.Usage
		if ev.ContextUsed > 0 {
			m.statusData.ContextUsed = ev.ContextUsed
			m.statusData.ContextEstimated = ev.ContextEstimated
		}
		if ev.ContextLimit > 0 {
			m.statusData.ContextLimit = ev.ContextLimit
		}
		m.liveStatsStartedAt = time.Time{}
		m.liveStatsOutputChars = 0
		m.sessionUsage.InputTokens += ev.Usage.InputTokens
		m.sessionUsage.OutputTokens += ev.Usage.OutputTokens
		m.syncStatusData()
		m.resizeChatViewport()
		if !m.debugEnabled {
			return m, m.beginProviderDiagnosticsFetch(false)
		}
		if ev.Duration > 0 {
			m.appendTools("", fmt.Sprintf("  %.1fs", ev.Duration.Seconds()))
			if ev.Usage.InputTokens > 0 {
				m.appendTools("", fmt.Sprintf(" • %d in / %d out", ev.Usage.InputTokens, ev.Usage.OutputTokens))
			}
			m.appendTools("", "\n")
		}
		return m, m.beginProviderDiagnosticsFetch(false)
	case llm.EventProgress:
		if strings.Contains(strings.ToLower(ev.Text), "applying") && strings.Contains(strings.ToLower(ev.Text), "queued input") {
			m.pendingQueuedInput = nil
		}
		if line := m.progressEventLine(ev); line != "" {
			if isCompactionProgressLine(line) {
				m.emitProgressCheckpoint("compaction:"+line, line)
			}
			m.UpdateRecentActivity(ev.Agent, line)
		}
	}
	// Auto-scroll tools pane when content is added.
	if ev.Kind == llm.EventToolCall || ev.Kind == llm.EventToolResult || ev.Kind == llm.EventStats {
		m.toolsScroll = m.toolsMaxScroll()
	}
	return m, nil
}

func (m ChatModel) handleAgentTaskEvent(ev llm.Event) (tea.Model, tea.Cmd) {
	payload := strings.TrimSpace(ev.Content)
	if payload == "" {
		payload = strings.TrimSpace(ev.Text)
	}
	if payload == "" {
		return m, nil
	}
	var task chatAgentTaskState
	if err := json.Unmarshal([]byte(payload), &task); err != nil || strings.TrimSpace(task.ID) == "" {
		return m, nil
	}
	m.upsertAgentTaskState(task)
	if !m.toolsVisible && !m.agentPanelHiddenByUser {
		m.toolsVisible = true
		m.toolsWasShowing = true
		m.resizeChatViewport()
		m.viewportDirty = true
	}
	m.toolsScroll = m.toolsMaxScroll()
	return m, nil
}

func (m *ChatModel) upsertAgentTaskState(task chatAgentTaskState) {
	id := strings.TrimSpace(task.ID)
	if id == "" {
		return
	}
	task.ID = id
	for i := range m.agentTasks {
		if strings.TrimSpace(m.agentTasks[i].ID) == id {
			m.agentTasks[i] = mergeChatAgentTaskState(m.agentTasks[i], task)
			return
		}
	}
	m.agentTasks = append(m.agentTasks, task)
}

func mergeChatAgentTaskState(existing, next chatAgentTaskState) chatAgentTaskState {
	merged := existing
	merged.ID = strings.TrimSpace(next.ID)
	if role := strings.TrimSpace(next.Role); role != "" {
		merged.Role = role
	}
	if status := strings.TrimSpace(next.Status); status != "" {
		merged.Status = status
	}
	if !next.CreatedAt.IsZero() {
		merged.CreatedAt = next.CreatedAt
	}
	if !next.StartedAt.IsZero() {
		merged.StartedAt = next.StartedAt
	}
	if !next.CompletedAt.IsZero() {
		merged.CompletedAt = next.CompletedAt
	}
	if !next.LastActivityAt.IsZero() {
		merged.LastActivityAt = next.LastActivityAt
	}
	if result := strings.TrimSpace(next.Result); result != "" {
		merged.Result = result
	}
	if errText := strings.TrimSpace(next.Error); errText != "" {
		merged.Error = errText
	}
	if tool := strings.TrimSpace(next.LastToolName); tool != "" {
		merged.LastToolName = tool
	}
	if len(next.RecentActivity) > 0 {
		merged.RecentActivity = append([]chatAgentTaskActivity(nil), next.RecentActivity...)
	}
	return merged
}

// handleSubAgentEvent routes all sub-agent activity to the tools pane with
// the agent role as a visible header. Human-readable prose is also mirrored
// into the main chat transcript for a transcript-first experience.
func (m ChatModel) handleSubAgentEvent(ev llm.Event) (tea.Model, tea.Cmd) {
	label := ev.SubAgent
	m.activeSubAgent = label
	if strings.TrimSpace(label) != "" && !m.toolsVisible && !m.agentPanelHiddenByUser {
		m.toolsVisible = true
		m.toolsWasShowing = true
		m.resizeChatViewport()
		m.viewportDirty = true
	}

	// Detect start/done/cancelled lifecycle messages from the sub-agent renderer.
	if ev.Kind == llm.EventToolCall && ev.Agent == "runtime" {
		if strings.Contains(ev.Text, "] starting") {
			m.toolsSections = append(m.toolsSections, toolsSection{role: label})
			sec := &m.toolsSections[len(m.toolsSections)-1]
			sec.buf = fmt.Sprintf("┌─ %s ─────────────────\n", label)
			m.status = label
			m.toolsScroll = m.toolsMaxScroll()
			return m, nil
		}
		if strings.Contains(ev.Text, "] done") || strings.Contains(ev.Text, "] cancelled") {
			for i := len(m.toolsSections) - 1; i >= 0; i-- {
				if m.toolsSections[i].role == label {
					sec := &m.toolsSections[i]
					status := "complete"
					if strings.Contains(ev.Text, "cancelled") {
						status = "cancelled"
					}
					sec.buf += fmt.Sprintf("└─ %s %s ────────\n\n", label, status)
					sec.summary = fmt.Sprintf("─ %s (%d turns, %d tools) %s ─\n", label, sec.turnCount, sec.toolCount, status)
					sec.collapsed = true
					m.pendingSubAgentSummary = &subAgentSummary{
						role:              label,
						turns:             sec.turnCount,
						tools:             sec.toolCount,
						transcriptVisible: sec.transcriptVisible,
					}
					break
				}
			}
			m.activeSubAgent = ""
			m.status = "running"
			m.toolsScroll = m.toolsMaxScroll()
			return m, nil
		}
	}

	switch ev.Kind {
	case llm.EventToken:
		sec := m.currentToolsSection(label)
		sec.tokenRun += ev.Text
		m.appendTools(label, ev.Text)
		if strings.TrimSpace(sec.tokenRun) != "" && !looksLikeStructuredTranscript(sec.tokenRun) {
			m.AppendToLastAgentLabeled(ev.Text, label)
			sec.transcriptVisible = true
		}
	case llm.EventToolCall:
		sec := m.currentToolsSection(label)
		sec.tokenRun = ""
		if sec.buf != "" && !strings.HasSuffix(sec.buf, "\n\n") {
			sec.buf += "\n"
		}
		m.appendTools(label, fmt.Sprintf("  │ %s › %s\n", label, ev.Agent))
		m.appendTools(label, fmt.Sprintf("  │   %s\n", ev.Text))
		sec.toolCount++
	case llm.EventToolResult:
		sec := m.currentToolsSection(label)
		sec.tokenRun = ""
		if ev.IsError {
			m.appendTools(label, fmt.Sprintf("  │   ✗ %s\n", truncate(ev.Text, 200)))
		} else {
			m.appendTools(label, fmt.Sprintf("  │   ✓ %s\n", truncate(ev.Text, 200)))
		}
	case llm.EventProgress:
		if line := strings.TrimSpace(ev.Text); line != "" {
			m.UpdateRecentActivity(label, line)
		}
	case llm.EventStats:
		sec := m.currentToolsSection(label)
		sec.tokenRun = ""
		m.sessionUsage.InputTokens += ev.Usage.InputTokens
		m.sessionUsage.OutputTokens += ev.Usage.OutputTokens
		if ev.Duration > 0 {
			m.appendTools(label, fmt.Sprintf("  │ %.1fs", ev.Duration.Seconds()))
			if ev.Usage.InputTokens > 0 {
				m.appendTools(label, fmt.Sprintf(" • %d in / %d out", ev.Usage.InputTokens, ev.Usage.OutputTokens))
			}
			m.appendTools(label, "\n")
		}
		for i := len(m.toolsSections) - 1; i >= 0; i-- {
			if m.toolsSections[i].role == label {
				m.toolsSections[i].turnCount++
				break
			}
		}
	case llm.EventError:
		sec := m.currentToolsSection(label)
		sec.tokenRun = ""
		m.appendTools(label, fmt.Sprintf("  │ ✗ [%s] %s\n", label, ev.Text))
	}
	// Auto-scroll tools pane to follow new output.
	m.toolsScroll = m.toolsMaxScroll()
	return m, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func runtimeStatusMessage(ev llm.Event) string {
	if status := previewStatusMessage(ev); status != "" {
		return status
	}
	return commandSessionStatusMessage(ev)
}

func previewStatusMessage(ev llm.Event) string {
	if ev.IsError {
		return ""
	}
	switch ev.Agent {
	case "preview_server_ensure", "preview_server_status":
	default:
		return ""
	}

	var payload struct {
		Status string `json:"status"`
		URL    string `json:"url"`
		Reused bool   `json:"reused"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(ev.Text)), &payload); err != nil {
		return ""
	}
	if strings.TrimSpace(payload.URL) == "" {
		return ""
	}
	switch ev.Agent {
	case "preview_server_ensure":
		if payload.Reused {
			return "preview still live at " + payload.URL
		}
		return "preview live at " + payload.URL
	case "preview_server_status":
		return "preview status: " + payload.URL
	default:
		return ""
	}
}

func commandSessionStatusMessage(ev llm.Event) string {
	if ev.IsError {
		return ""
	}
	switch ev.Agent {
	case "run_command", "command_status", "exec_session_start", "exec_session_status", "exec_session_write", "exec_session_resize", "exec_session_stop":
	default:
		return ""
	}

	var payload struct {
		Status    string `json:"status"`
		SessionID int    `json:"session_id"`
		Command   string `json:"command"`
		Output    string `json:"output"`
		ExitCode  int    `json:"exit_code"`
		PTY       bool   `json:"pty"`
		Cols      int    `json:"cols"`
		Rows      int    `json:"rows"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(ev.Text)), &payload); err != nil {
		return ""
	}
	if payload.SessionID == 0 || strings.TrimSpace(payload.Status) == "" {
		return ""
	}
	command := strings.TrimSpace(payload.Command)
	sessionLabel := "command session"
	if payload.PTY || strings.HasPrefix(strings.TrimSpace(ev.Agent), "exec_session_") {
		sessionLabel = "terminal session"
	}
	sizeLabel := ""
	if payload.Cols > 0 && payload.Rows > 0 {
		sizeLabel = fmt.Sprintf("%dx%d", payload.Cols, payload.Rows)
	}
	switch payload.Status {
	case "running":
		if ev.Agent == "exec_session_resize" && sizeLabel != "" {
			if command == "" {
				return fmt.Sprintf("%s %d resized to %s", sessionLabel, payload.SessionID, sizeLabel)
			}
			return fmt.Sprintf("%s %d resized to %s: %s", sessionLabel, payload.SessionID, sizeLabel, command)
		}
		output := summarizeCommandSessionOutput(payload.Output)
		if command == "" {
			if output == "" {
				return fmt.Sprintf("%s %d running", sessionLabel, payload.SessionID)
			}
			return fmt.Sprintf("%s %d running\n  └ %s", sessionLabel, payload.SessionID, output)
		}
		if output == "" {
			return fmt.Sprintf("%s %d running: %s", sessionLabel, payload.SessionID, command)
		}
		return fmt.Sprintf("%s %d running: %s\n  └ %s", sessionLabel, payload.SessionID, command, output)
	case "exited":
		output := summarizeCommandSessionOutput(payload.Output)
		if command == "" {
			if output == "" {
				return fmt.Sprintf("%s %d exited with code %d", sessionLabel, payload.SessionID, payload.ExitCode)
			}
			return fmt.Sprintf("%s %d exited with code %d\n  └ %s", sessionLabel, payload.SessionID, payload.ExitCode, output)
		}
		if output == "" {
			return fmt.Sprintf("%s %d exited with code %d: %s", sessionLabel, payload.SessionID, payload.ExitCode, command)
		}
		return fmt.Sprintf("%s %d exited with code %d: %s\n  └ %s", sessionLabel, payload.SessionID, payload.ExitCode, command, output)
	default:
		return ""
	}
}

func summarizeCommandSessionOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	parts := make([]string, 0, min(2, len(lines)))
	for _, line := range lines {
		line = compactStatusText(line)
		if line == "" {
			continue
		}
		parts = append(parts, truncate(line, 120))
		if len(parts) == 2 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	summary := strings.Join(parts, " | ")
	if len(lines) > len(parts) {
		summary += fmt.Sprintf(" | … +%d lines", len(lines)-len(parts))
	}
	return summary
}

func latestFencedCodeBlock(content string) string {
	parts := strings.Split(content, "```")
	if len(parts) < 3 {
		return ""
	}
	for i := len(parts) - 2; i >= 1; i -= 2 {
		block := strings.TrimLeft(parts[i], "\n")
		if newline := strings.Index(block, "\n"); newline >= 0 {
			block = block[newline+1:]
		}
		block = strings.TrimSpace(block)
		if block != "" {
			return block
		}
	}
	return ""
}

func (m *ChatModel) resetProviderDiagnostics() {
	m.statusData.CopilotLive = nil
	m.statusData.CodexUsage = nil
	m.statsCopilotLoading = false
	m.statsCodexLoading = false
	m.statsCopilotErr = ""
	m.statsCodexErr = ""
}

func (m *ChatModel) beginProviderDiagnosticsFetch(force bool) tea.Cmd {
	provider := providerFromModel(m.model)
	var cmds []tea.Cmd

	if provider == "copilot" && m.config.FetchLiveCopilotQuota != nil && (force || (m.statusData.CopilotLive == nil && !m.statsCopilotLoading)) {
		m.statsCopilotLoading = true
		if force {
			m.statsCopilotErr = ""
		}
		model := m.model
		fetch := m.config.FetchLiveCopilotQuota
		cmds = append(cmds, func() tea.Msg {
			quota, err := fetch(context.Background())
			return statsCopilotQuotaMsg{model: model, quota: quota, err: err}
		})
	}

	if (provider == "chatgpt" || provider == "openai" || provider == "codex") && m.config.FetchCodexUsage != nil && (force || (m.statusData.CodexUsage == nil && !m.statsCodexLoading)) {
		m.statsCodexLoading = true
		if force {
			m.statsCodexErr = ""
		}
		model := m.model
		fetch := m.config.FetchCodexUsage
		cmds = append(cmds, func() tea.Msg {
			snapshot, err := fetch(context.Background())
			return statsCodexUsageMsg{model: model, snapshot: snapshot, err: err}
		})
	}

	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
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
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.Type {
	case tea.KeyCtrlF:
		m.openSearchOverlay("")
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
			return m, tea.Quit
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
			return m, tea.Quit
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
		return m, tea.Quit, true
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

	// Auto-skill detection
	if !m.busy && input != "" {
		switch m.autoSkillsMode {
		case skills.AutoSkillsAuto:
			if s, ok := skills.DetectAuto(m.skills, input); ok {
				if m.state != nil && m.state.SkillActivated(s.Name) {
					break
				}
				updated, submitCmd := m.submitSkillInput(s, input, input)
				return updated.(ChatModel), submitCmd, true
			}
		case "", skills.AutoSkillsSuggest:
			if s, ok := skills.DetectAuto(m.skills, input); ok {
				m.flash = fmt.Sprintf("suggested skill: /%s", s.Name)
			}
		}
	}

	// Required skill check
	requiredSkill := skills.RequiredForInput(input)
	if requiredSkill != "" && !m.state.SkillActivated(requiredSkill) {
		if _, ok := skills.Get(m.skills, requiredSkill); ok {
			m.flash = fmt.Sprintf("required skill: /%s", requiredSkill)
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
	"/skills", "/auto-skills", "/sessions", "/save", "/restore",
	"/find", "/files", "/copy agent", "/copy tools", "/copy code", "/copy result",
	"/exit", "/quit",
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
		_ = m.saveSession("last-session")
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
			m.flash = fmt.Sprintf("session restored: %s", name)
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
			m.flash = fmt.Sprintf("session restored: %s", name)
		}
	case input == "/tools" || input == "/toggle tools":
		m.toolsVisible = false
		m.flash = "tools pane removed"
	case input == "/toggle tools on":
		m.toolsVisible = false
		m.flash = "tools pane removed"
	case input == "/toggle tools off":
		m.toolsVisible = false
		m.flash = "tools pane removed"
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
	case input == "/auto-skills":
		mode := skills.NormalizeAutoMode(m.autoSkillsMode)
		if mode == "" {
			mode = skills.AutoSkillsSuggest
		}
		m.flash = fmt.Sprintf("auto-skills: %s", mode)
	case strings.HasPrefix(input, "/auto-skills "):
		mode := skills.NormalizeAutoMode(strings.TrimSpace(strings.TrimPrefix(input, "/auto-skills ")))
		if mode == "" {
			m.flash = "auto-skills must be one of: off, suggest, auto"
			break
		}
		m.autoSkillsMode = mode
		m.flash = fmt.Sprintf("auto-skills: %s", mode)
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
	default:
		m.flash = "unknown command: " + input
	}
	return m, nil
}

func (m ChatModel) helpTabs() []string {
	return []string{"Keys", "Chat Commands", "CLI Skills"}
}

func (m ChatModel) helpLines() []string {
	switch m.helpTab {
	case 1:
		return []string{
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
			"  /auto-skills       show auto-skills mode",
			"  /auto-skills <m>   set off, suggest, or auto",
			"",
			"Session state:",
			"  /new               start a clean session",
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
		}
	case 2:
		return []string{
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
		}
	default:
		return []string{
			"Keyboard shortcuts",
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
			"",
			"Turn control:",
			"  Esc                cancel current run",
		}
	}
}

func (m *ChatModel) openSearchOverlay(query string) {
	m.searchPane = m.paneFocus
	m.searchVisible = true
	m.searchQuery = query
	m.searchPos = len([]rune(query))
	m.updateSearchMatches(false)
}

func (m *ChatModel) searchTarget() ([]string, *int, int, string) {
	switch m.searchPane {
	case focusTools:
		lines := m.toolsWrappedLines()
		if len(lines) == 0 {
			lines = []string{""}
		}
		return lines, &m.toolsScroll, max(1, m.chatViewport.Height), "tools"
	default:
		lines := strings.Split(m.chatContent, "\n")
		if len(lines) == 0 {
			lines = []string{""}
		}
		return lines, &m.chatViewport.YOffset, max(1, m.chatViewport.Height), "agent"
	}
}

func (m *ChatModel) updateSearchMatches(jump bool) {
	query := strings.TrimSpace(strings.ToLower(m.searchQuery))
	m.searchMatches = nil
	m.searchCurrent = -1
	if query == "" {
		return
	}
	lines, scroll, visible, paneName := m.searchTarget()
	for idx, line := range lines {
		if strings.Contains(strings.ToLower(line), query) {
			m.searchMatches = append(m.searchMatches, idx)
		}
	}
	if len(m.searchMatches) == 0 {
		m.flash = fmt.Sprintf("no matches for %q", m.searchQuery)
		return
	}
	m.searchCurrent = 0
	line := m.searchMatches[0]
	*scroll = clamp(line-(visible/2), 0, max(0, len(lines)-visible))
	m.flash = fmt.Sprintf("%d match(es) in %s pane", len(m.searchMatches), paneName)
	if jump && len(m.searchMatches) > 1 {
		m.searchNext(1)
	}
}

func (m *ChatModel) openFilePicker(query string) {
	list, err := discoverContextFiles(m.workDir)
	if err != nil {
		m.flash = fmt.Sprintf("file picker failed: %v", err)
		return
	}
	m.filesVisible = true
	m.filesBrowser = false
	m.filesViewing = false
	m.filesList = list
	m.filesQuery = query
	m.filesPos = len([]rune(query))
	m.filesCursor = 0
	m.filesViewPath = ""
	m.filesViewText = ""
	m.filesViewScroll = 0
	m.updateFilePickerMatches()
}

func (m *ChatModel) openWorkspaceFileBrowser(query string) {
	m.openFilePicker(query)
	m.filesBrowser = true
	m.flash = "files opened"
}

func (m *ChatModel) updateFilePickerMatches() {
	query := strings.TrimSpace(strings.ToLower(m.filesQuery))
	filtered := make([]string, 0, len(m.filesList))
	for _, path := range m.filesList {
		if query == "" || strings.Contains(strings.ToLower(path), query) {
			filtered = append(filtered, path)
		}
	}
	m.filesFiltered = filtered
	if len(filtered) == 0 {
		m.filesCursor = 0
		return
	}
	m.filesCursor = clamp(m.filesCursor, 0, len(filtered)-1)
}

func (m *ChatModel) replaceActiveAtToken(path string) {
	runes := []rune(m.inputBuf)
	if m.inputPos < 0 || m.inputPos > len(runes) {
		return
	}
	start := m.inputPos
	for start > 0 {
		r := runes[start-1]
		if r == '@' {
			start--
			break
		}
		if r == ' ' || r == '\t' || r == '\n' {
			return
		}
		start--
	}
	if start < 0 || start >= len(runes) || runes[start] != '@' {
		return
	}
	end := m.inputPos
	for end < len(runes) {
		r := runes[end]
		if r == ' ' || r == '\t' || r == '\n' {
			break
		}
		end++
	}
	repl := "@" + path + " "
	prefix := string(runes[:start])
	m.inputBuf = prefix + repl + string(runes[end:])
	m.inputPos = len([]rune(prefix + repl))
	already := false
	for _, existing := range m.contextFiles {
		if existing == path {
			already = true
			break
		}
	}
	if !already {
		m.contextFiles = append(m.contextFiles, path)
		sort.Strings(m.contextFiles)
	}
	m.filesVisible = false
	m.filesViewing = false
	m.flash = fmt.Sprintf("added context %s", path)
}

func (m *ChatModel) addContextFile(path string) {
	if path == "" {
		return
	}
	already := false
	for _, existing := range m.contextFiles {
		if existing == path {
			already = true
			break
		}
	}
	if !already {
		m.contextFiles = append(m.contextFiles, path)
		sort.Strings(m.contextFiles)
	}
	m.flash = fmt.Sprintf("added context %s", path)
}

func (m *ChatModel) openFileViewer(path string) {
	resolved, err := tools.ResolvePath(m.workDir, path)
	if err != nil {
		m.flash = fmt.Sprintf("open failed: %v", err)
		return
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		m.filesViewText = fmt.Sprintf("error reading file: %v", err)
	} else if tools.IsBinary(data) {
		m.filesViewText = "binary file preview unavailable"
	} else {
		m.filesViewText = string(data)
	}
	m.filesViewing = true
	m.filesViewPath = path
	m.filesViewScroll = 0
}

func (m ChatModel) handleFilePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filesViewing {
		return m.handleFileViewerKey(msg)
	}
	switch msg.Type {
	case tea.KeyEscape:
		m.filesVisible = false
	case tea.KeyEnter:
		if m.filesBrowser {
			if m.filesCursor >= 0 && m.filesCursor < len(m.filesFiltered) {
				m.openFileViewer(m.filesFiltered[m.filesCursor])
			}
			return m, nil
		}
		if rel := m.resolveExplicitContextPath(strings.TrimSpace(m.filesQuery)); rel != "" {
			m.replaceActiveAtToken(rel)
			return m, nil
		}
		if m.filesCursor >= 0 && m.filesCursor < len(m.filesFiltered) {
			m.replaceActiveAtToken(m.filesFiltered[m.filesCursor])
		}
	case tea.KeyUp:
		if m.filesCursor > 0 {
			m.filesCursor--
		}
	case tea.KeyDown:
		if m.filesCursor < len(m.filesFiltered)-1 {
			m.filesCursor++
		}
	case tea.KeyLeft:
		if m.filesPos > 0 {
			m.filesPos--
		}
	case tea.KeyRight:
		if m.filesPos < len([]rune(m.filesQuery)) {
			m.filesPos++
		}
	case tea.KeyBackspace:
		if m.filesPos > 0 {
			runes := []rune(m.filesQuery)
			m.filesQuery = string(append(runes[:m.filesPos-1], runes[m.filesPos:]...))
			m.filesPos--
			m.updateFilePickerMatches()
		}
	case tea.KeyDelete:
		runes := []rune(m.filesQuery)
		if m.filesPos < len(runes) {
			m.filesQuery = string(append(runes[:m.filesPos], runes[m.filesPos+1:]...))
			m.updateFilePickerMatches()
		}
	case tea.KeyCtrlA:
		m.filesPos = 0
	case tea.KeyCtrlE:
		m.filesPos = len([]rune(m.filesQuery))
	case tea.KeyCtrlU:
		m.filesQuery = ""
		m.filesPos = 0
		m.updateFilePickerMatches()
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'q' || msg.Runes[0] == 'Q') {
			m.filesVisible = false
			return m, nil
		}
		if m.filesBrowser && len(msg.Runes) == 1 && msg.Runes[0] == '@' {
			if m.filesCursor >= 0 && m.filesCursor < len(m.filesFiltered) {
				m.addContextFile(m.filesFiltered[m.filesCursor])
			}
			return m, nil
		}
		for _, r := range msg.Runes {
			runes := []rune(m.filesQuery)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.filesPos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.filesPos:]...)
			m.filesQuery = string(newRunes)
			m.filesPos++
		}
		m.updateFilePickerMatches()
	}
	return m, nil
}

func (m ChatModel) handleFileViewerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	_, _, _, _, _, contentHeight, _ := m.filesOverlayLayout()
	maxScroll := max(0, len(strings.Split(m.filesViewText, "\n"))-contentHeight)
	scrollBy := func(delta int) {
		m.filesViewScroll = clamp(m.filesViewScroll+delta, 0, maxScroll)
	}
	switch msg.Type {
	case tea.KeyEscape:
		m.filesViewing = false
	case tea.KeyUp:
		scrollBy(-1)
	case tea.KeyDown:
		scrollBy(1)
	case tea.KeyPgUp:
		scrollBy(-max(1, contentHeight-1))
	case tea.KeyPgDown:
		scrollBy(max(1, contentHeight-1))
	case tea.KeyHome:
		m.filesViewScroll = 0
	case tea.KeyEnd:
		m.filesViewScroll = maxScroll
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'q', 'Q':
				m.filesViewing = false
			case 'j', 'J':
				scrollBy(1)
			case 'k', 'K':
				scrollBy(-1)
			case 'g':
				m.filesViewScroll = 0
			case 'G':
				m.filesViewScroll = maxScroll
			case 'a', 'A', '@':
				m.addContextFile(m.filesViewPath)
			}
		}
	}
	return m, nil
}

func (m ChatModel) resolveExplicitContextPath(query string) string {
	if query == "" {
		return ""
	}
	resolved, err := tools.ResolvePath(m.workDir, query)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(resolved); err != nil {
		return ""
	}
	resolvedWorkDir, err := filepath.EvalSymlinks(m.workDir)
	if err != nil {
		resolvedWorkDir = filepath.Clean(m.workDir)
	}
	rel, err := filepath.Rel(resolvedWorkDir, resolved)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func discoverContextFiles(workDir string) ([]string, error) {
	var files []string
	walkErr := filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".swp") {
			return nil
		}
		rel, relErr := filepath.Rel(workDir, path)
		if relErr != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Strings(files)
	return files, nil
}

func (m *ChatModel) searchNext(delta int) bool {
	if len(m.searchMatches) == 0 {
		return false
	}
	lines, scroll, visible, _ := m.searchTarget()
	if m.searchCurrent < 0 {
		m.searchCurrent = 0
	} else {
		m.searchCurrent = (m.searchCurrent + delta + len(m.searchMatches)) % len(m.searchMatches)
	}
	line := m.searchMatches[m.searchCurrent]
	*scroll = clamp(line-(visible/2), 0, max(0, len(lines)-visible))
	m.flash = fmt.Sprintf("match %d/%d for %q", m.searchCurrent+1, len(m.searchMatches), m.searchQuery)
	return true
}

func (m ChatModel) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.searchVisible = false
	case tea.KeyEnter:
		m.updateSearchMatches(true)
		m.searchVisible = false
	case tea.KeyLeft:
		if m.searchPos > 0 {
			m.searchPos--
		}
	case tea.KeyRight:
		if m.searchPos < len([]rune(m.searchQuery)) {
			m.searchPos++
		}
	case tea.KeyBackspace:
		if m.searchPos > 0 {
			runes := []rune(m.searchQuery)
			m.searchQuery = string(append(runes[:m.searchPos-1], runes[m.searchPos:]...))
			m.searchPos--
			m.updateSearchMatches(false)
		}
	case tea.KeyDelete:
		runes := []rune(m.searchQuery)
		if m.searchPos < len(runes) {
			m.searchQuery = string(append(runes[:m.searchPos], runes[m.searchPos+1:]...))
			m.updateSearchMatches(false)
		}
	case tea.KeyCtrlA:
		m.searchPos = 0
	case tea.KeyCtrlE:
		m.searchPos = len([]rune(m.searchQuery))
	case tea.KeyCtrlU:
		m.searchQuery = ""
		m.searchPos = 0
		m.updateSearchMatches(false)
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			runes := []rune(m.searchQuery)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.searchPos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.searchPos:]...)
			m.searchQuery = string(newRunes)
			m.searchPos++
		}
		m.updateSearchMatches(false)
	}
	return m, nil
}

func (m ChatModel) handleStatsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape, tea.KeyEnter:
		m.statsVisible = false
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'q' || msg.Runes[0] == 'Q') {
			m.statsVisible = false
		}
	}
	return m, nil
}

func (m ChatModel) handleTraceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape, tea.KeyEnter:
		m.traceVisible = false
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'q' || msg.Runes[0] == 'Q') {
			m.traceVisible = false
		}
	}
	return m, nil
}

func (m *ChatModel) refreshSessionsPicker(resetCursor bool) bool {
	sessions, err := listChatSessions()
	if err != nil {
		m.flash = fmt.Sprintf("sessions failed: %v", err)
		return false
	}
	if len(sessions) == 0 {
		m.sessionsList = nil
		m.sessionsVisible = false
		m.sessionRenaming = false
		m.sessionsCursor = 0
		m.flash = "no saved sessions"
		return false
	}
	currentName := ""
	if !resetCursor && m.sessionsCursor >= 0 && m.sessionsCursor < len(m.sessionsList) {
		currentName = m.sessionsList[m.sessionsCursor].name
	}
	m.sessionsList = sessions
	m.sessionsVisible = true
	m.sessionRenaming = false
	if resetCursor {
		m.sessionsCursor = 0
		return true
	}
	for i, entry := range sessions {
		if entry.name == currentName {
			m.sessionsCursor = i
			return true
		}
	}
	m.sessionsCursor = clamp(m.sessionsCursor, 0, len(sessions)-1)
	return true
}

func (m ChatModel) handleSessionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.sessionRenaming {
		return m.handleSessionRenameKey(msg)
	}
	switch msg.Type {
	case tea.KeyEscape:
		m.sessionsVisible = false
	case tea.KeyUp:
		if m.sessionsCursor > 0 {
			m.sessionsCursor--
		}
	case tea.KeyDown:
		if m.sessionsCursor < len(m.sessionsList)-1 {
			m.sessionsCursor++
		}
	case tea.KeyEnter:
		m.restorePickedSession(m.sessionsCursor)
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= '1' && r <= '9' {
				idx := int(r - '1')
				if idx < len(m.sessionsList) {
					m.sessionsCursor = idx
					m.restorePickedSession(idx)
				}
				return m, nil
			}
			switch r {
			case 'd', 'D':
				m.deletePickedSession(m.sessionsCursor)
			case 'r', 'R':
				m.beginRenamePickedSession(m.sessionsCursor)
			}
		}
	}
	return m, nil
}

func (m *ChatModel) restorePickedSession(idx int) {
	if idx < 0 || idx >= len(m.sessionsList) {
		return
	}
	name := m.sessionsList[idx].name
	if err := m.restoreSession(name); err != nil {
		m.flash = fmt.Sprintf("restore failed: %v", err)
		return
	}
	m.sessionsVisible = false
	m.flash = fmt.Sprintf("session restored: %s", name)
}

func (m *ChatModel) beginRenamePickedSession(idx int) {
	if idx < 0 || idx >= len(m.sessionsList) {
		return
	}
	name := m.sessionsList[idx].name
	if name == "last-session" {
		m.flash = "cannot rename last-session"
		return
	}
	m.sessionRenaming = true
	m.sessionRenameBuf = name
	m.sessionRenamePos = len([]rune(name))
}

func (m ChatModel) handleSessionRenameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.sessionRenaming = false
	case tea.KeyEnter:
		m.commitRenamePickedSession()
	case tea.KeyLeft:
		if m.sessionRenamePos > 0 {
			m.sessionRenamePos--
		}
	case tea.KeyRight:
		if m.sessionRenamePos < len([]rune(m.sessionRenameBuf)) {
			m.sessionRenamePos++
		}
	case tea.KeyBackspace:
		if m.sessionRenamePos > 0 {
			runes := []rune(m.sessionRenameBuf)
			m.sessionRenameBuf = string(append(runes[:m.sessionRenamePos-1], runes[m.sessionRenamePos:]...))
			m.sessionRenamePos--
		}
	case tea.KeyDelete:
		runes := []rune(m.sessionRenameBuf)
		if m.sessionRenamePos < len(runes) {
			m.sessionRenameBuf = string(append(runes[:m.sessionRenamePos], runes[m.sessionRenamePos+1:]...))
		}
	case tea.KeyCtrlA:
		m.sessionRenamePos = 0
	case tea.KeyCtrlE:
		m.sessionRenamePos = len([]rune(m.sessionRenameBuf))
	case tea.KeyCtrlU:
		m.sessionRenameBuf = ""
		m.sessionRenamePos = 0
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			runes := []rune(m.sessionRenameBuf)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.sessionRenamePos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.sessionRenamePos:]...)
			m.sessionRenameBuf = string(newRunes)
			m.sessionRenamePos++
		}
	}
	return m, nil
}

func (m *ChatModel) commitRenamePickedSession() {
	if m.sessionsCursor < 0 || m.sessionsCursor >= len(m.sessionsList) {
		m.sessionRenaming = false
		return
	}
	oldName := m.sessionsList[m.sessionsCursor].name
	newName := sanitizeChatSessionName(m.sessionRenameBuf)
	if newName == "" {
		m.flash = "rename failed: missing session name"
		return
	}
	if oldName == newName {
		m.sessionRenaming = false
		return
	}
	if err := renameChatSession(oldName, newName); err != nil {
		m.flash = fmt.Sprintf("rename failed: %v", err)
		return
	}
	m.flash = fmt.Sprintf("session renamed: %s -> %s", oldName, newName)
	if !m.refreshSessionsPicker(false) {
		return
	}
	for i, entry := range m.sessionsList {
		if entry.name == newName {
			m.sessionsCursor = i
			break
		}
	}
}

func (m *ChatModel) deletePickedSession(idx int) {
	if idx < 0 || idx >= len(m.sessionsList) {
		return
	}
	name := m.sessionsList[idx].name
	if name == "last-session" {
		m.flash = "cannot delete last-session"
		return
	}
	if err := deleteChatSession(name); err != nil {
		m.flash = fmt.Sprintf("delete failed: %v", err)
		return
	}
	m.flash = fmt.Sprintf("session deleted: %s", name)
	if !m.refreshSessionsPicker(false) {
		return
	}
	m.sessionsCursor = clamp(idx, 0, len(m.sessionsList)-1)
}

func (m *ChatModel) openModelPicker() {
	m.ensureModelListLoaded()
	m.modelsQuery = ""
	m.modelsQueryPos = 0
	m.updateModelFilter()
	m.modelsVisible = true
	m.modelsCursor = 0
}

func (m *ChatModel) updateModelFilter() {
	query := strings.TrimSpace(strings.ToLower(m.modelsQuery))
	m.modelsFiltered = m.modelsFiltered[:0]
	for _, name := range m.modelsList {
		label := strings.ToLower(m.modelOptionLabel(name))
		if query == "" || strings.Contains(strings.ToLower(name), query) || strings.Contains(label, query) {
			m.modelsFiltered = append(m.modelsFiltered, name)
		}
	}
	if len(m.modelsFiltered) == 0 {
		m.modelsCursor = 0
		return
	}
	m.modelsCursor = clamp(m.modelsCursor, 0, len(m.modelsFiltered)-1)
}

func (m ChatModel) modelOptionLabel(name string) string {
	if m.config.DescribeModel != nil {
		if label := strings.TrimSpace(m.config.DescribeModel(name)); label != "" {
			return label
		}
	}
	return name
}

func (m ChatModel) uniqueModelOptions(models []string) []string {
	models = uniqueStringsPreserveOrder(models)
	out := make([]string, 0, len(models))
	seen := make(map[string]int, len(models))
	for _, name := range models {
		label := strings.TrimSpace(strings.ToLower(m.modelOptionLabel(name)))
		if label == "" {
			label = strings.TrimSpace(strings.ToLower(name))
		}
		if idx, ok := seen[label]; ok {
			if preferExplicitModelOption(name, out[idx]) {
				out[idx] = name
			}
			continue
		}
		seen[label] = len(out)
		out = append(out, name)
	}
	return out
}

func preferExplicitModelOption(candidate, current string) bool {
	candidateExplicit := strings.Contains(candidate, "/")
	currentExplicit := strings.Contains(current, "/")
	if candidateExplicit != currentExplicit {
		return candidateExplicit
	}
	return false
}

func (m ChatModel) handleModelsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.modelsVisible = false
	case tea.KeyUp:
		if m.modelsCursor > 0 {
			m.modelsCursor--
		}
	case tea.KeyDown:
		if m.modelsCursor < len(m.modelsFiltered)-1 {
			m.modelsCursor++
		}
	case tea.KeyLeft:
		if m.modelsQueryPos > 0 {
			m.modelsQueryPos--
		}
	case tea.KeyRight:
		if m.modelsQueryPos < len([]rune(m.modelsQuery)) {
			m.modelsQueryPos++
		}
	case tea.KeyBackspace:
		if m.modelsQueryPos > 0 {
			runes := []rune(m.modelsQuery)
			m.modelsQuery = string(append(runes[:m.modelsQueryPos-1], runes[m.modelsQueryPos:]...))
			m.modelsQueryPos--
			m.updateModelFilter()
		}
	case tea.KeyDelete:
		runes := []rune(m.modelsQuery)
		if m.modelsQueryPos < len(runes) {
			m.modelsQuery = string(append(runes[:m.modelsQueryPos], runes[m.modelsQueryPos+1:]...))
			m.updateModelFilter()
		}
	case tea.KeyCtrlA:
		m.modelsQueryPos = 0
	case tea.KeyCtrlE:
		m.modelsQueryPos = len([]rune(m.modelsQuery))
	case tea.KeyCtrlU:
		m.modelsQuery = ""
		m.modelsQueryPos = 0
		m.updateModelFilter()
	case tea.KeyEnter:
		return m, m.pickModel(m.modelsCursor)
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			runes := []rune(m.modelsQuery)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.modelsQueryPos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.modelsQueryPos:]...)
			m.modelsQuery = string(newRunes)
			m.modelsQueryPos++
		}
		m.updateModelFilter()
	}
	return m, nil
}

func (m *ChatModel) ensureModelListLoaded() {
	if len(m.modelsList) > 0 {
		m.modelsList = m.uniqueModelOptions(m.modelsList)
		return
	}
	if len(m.config.AvailableModels) > 0 {
		m.modelsList = m.uniqueModelOptions(m.config.AvailableModels)
		return
	}
	m.refreshModelList()
}

func (m *ChatModel) refreshModelList() {
	var models []string
	if m.config.RefreshModels != nil {
		models = m.config.RefreshModels()
	} else {
		models = m.config.AvailableModels
	}
	m.modelsList = m.uniqueModelOptions(models)
	m.updateModelFilter()
}

func (m *ChatModel) pickModel(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.modelsFiltered) {
		return nil
	}
	picked := m.modelsFiltered[idx]
	if m.config.SwitchModel != nil {
		newModel, err := m.config.SwitchModel(picked)
		if err != nil {
			m.flash = fmt.Sprintf("error: %v", err)
			return nil
		}
		m.model = newModel
		m.resetProviderDiagnostics()
		m.syncStatusData()
		m.flash = fmt.Sprintf("switched to %s", newModel)
	}
	m.modelsVisible = false
	return nil
}

func (m *ChatModel) openProviderPicker() {
	if m.config.RefreshProviders != nil {
		m.providersList = append([]ProviderOption(nil), m.config.RefreshProviders()...)
	} else {
		m.providersList = append([]ProviderOption(nil), m.config.Providers...)
	}
	m.providersVisible = true
	m.providerPromptingKey = false
	m.providerPromptLabel = ""
	m.providerPromptMasked = false
	m.providerStatus = ""
	m.providerAuthURL = ""
	m.providerAuthCode = ""
	m.providerAuthWaiting = false
	m.providerAuthProvider = ""
	m.providerAuthFlow = nil
	m.providersCursor = 0
}

func (m ChatModel) handleProvidersKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.providerAuthWaiting {
		switch msg.Type {
		case tea.KeyEscape:
			if m.providerAuthCancel != nil {
				m.providerAuthCancel()
				m.providerAuthCancel = nil
			}
			m.providerAuthWaiting = false
			m.providerAuthURL = ""
			m.providerAuthCode = ""
			m.providerAuthProvider = ""
			m.providerStatus = "sign-in canceled"
		}
		return m, nil
	}
	if m.providerPromptingKey {
		if m.providerAuthProvider == "claude" && strings.TrimSpace(m.providerAuthURL) != "" {
			switch msg.Type {
			case tea.KeyCtrlO:
				return m, openProviderAuthURL(m.providerAuthURL)
			case tea.KeyRunes:
				if len(msg.Runes) == 1 && (msg.Runes[0] == 'o' || msg.Runes[0] == 'O') && strings.TrimSpace(m.providerKeyInput) == "" {
					return m, openProviderAuthURL(m.providerAuthURL)
				}
			}
		}
		switch msg.Type {
		case tea.KeyEscape:
			m.providerPromptingKey = false
			m.providerPromptLabel = ""
			m.providerPromptMasked = false
			m.providerKeyInput = ""
			m.providerKeyPos = 0
			m.providerAuthProvider = ""
			m.providerAuthFlow = nil
			m.providerStatus = ""
		case tea.KeyEnter:
			return m.saveProviderKey()
		case tea.KeyLeft:
			if m.providerKeyPos > 0 {
				m.providerKeyPos--
			}
		case tea.KeyRight:
			if m.providerKeyPos < len([]rune(m.providerKeyInput)) {
				m.providerKeyPos++
			}
		case tea.KeyBackspace:
			if m.providerKeyPos > 0 {
				runes := []rune(m.providerKeyInput)
				m.providerKeyInput = string(append(runes[:m.providerKeyPos-1], runes[m.providerKeyPos:]...))
				m.providerKeyPos--
			}
		case tea.KeyDelete:
			runes := []rune(m.providerKeyInput)
			if m.providerKeyPos < len(runes) {
				m.providerKeyInput = string(append(runes[:m.providerKeyPos], runes[m.providerKeyPos+1:]...))
			}
		case tea.KeyRunes:
			for _, r := range msg.Runes {
				runes := []rune(m.providerKeyInput)
				newRunes := make([]rune, 0, len(runes)+1)
				newRunes = append(newRunes, runes[:m.providerKeyPos]...)
				newRunes = append(newRunes, r)
				newRunes = append(newRunes, runes[m.providerKeyPos:]...)
				m.providerKeyInput = string(newRunes)
				m.providerKeyPos++
			}
		}
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEscape:
		m.providersVisible = false
	case tea.KeyUp:
		if m.providersCursor > 0 {
			m.providersCursor--
		}
	case tea.KeyDown:
		if m.providersCursor < len(m.providersList)-1 {
			m.providersCursor++
		}
	case tea.KeyEnter:
		return m.activateProviderSelection()
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'd', 'D':
				return m.deleteProviderCredential()
			}
		}
	}
	return m, nil
}

func (m ChatModel) activateProviderSelection() (tea.Model, tea.Cmd) {
	if m.providersCursor < 0 || m.providersCursor >= len(m.providersList) {
		return m, nil
	}
	provider := m.providersList[m.providersCursor]
	if providerNeedsInteractiveLogin(provider) {
		return m.startProviderLogin(provider)
	}
	if providerUsesAPIKey(provider.ID) {
		m.providerPromptingKey = true
		m.providerKeyInput = ""
		m.providerKeyPos = 0
		m.providerPromptLabel = "API key"
		m.providerPromptMasked = true
		m.providerStatus = "enter API key"
		return m, nil
	}
	if provider.DefaultModel != "" && m.config.SwitchModel != nil {
		newModel, err := m.config.SwitchModel(provider.DefaultModel)
		if err != nil {
			m.flash = fmt.Sprintf("error: %v", err)
		} else {
			m.model = newModel
			m.resetProviderDiagnostics()
			m.syncStatusData()
			m.flash = fmt.Sprintf("switched to %s", newModel)
			m.providersVisible = false
			return m, nil
		}
	}
	m.providersVisible = false
	return m, nil
}

func providerNeedsInteractiveLogin(provider ProviderOption) bool {
	id := strings.ToLower(strings.TrimSpace(provider.ID))
	if id != "chatgpt" && id != "claude" && id != "copilot" {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(provider.Status))
	return strings.Contains(status, "sign in")
}

func (m ChatModel) startProviderLogin(provider ProviderOption) (tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(provider.ID)) {
	case "chatgpt":
		m.providerStatus = "requesting ChatGPT device code..."
		return m, func() tea.Msg {
			flow, err := startChatGPTDeviceAuth(context.Background())
			if err != nil {
				return providerAuthFailedMsg{providerID: provider.ID, err: err}
			}
			return providerAuthStartedMsg{
				providerID: provider.ID,
				verifyURL:  flow.VerificationURL(),
				userCode:   flow.UserCode(),
				flow:       flow,
			}
		}
	case "claude":
		m.providerStatus = "preparing Claude sign-in..."
		return m, func() tea.Msg {
			flow, err := startClaudeAuth()
			if err != nil {
				return providerAuthFailedMsg{providerID: provider.ID, err: err}
			}
			return providerAuthStartedMsg{
				providerID: provider.ID,
				verifyURL:  flow.AuthorizationURL,
				flow:       flow,
			}
		}
	case "copilot":
		if strings.TrimSpace(m.config.CopilotClientID) == "" {
			m.providerStatus = "missing Copilot client id"
			return m, nil
		}
		m.providerStatus = "requesting Copilot device code..."
		clientID := m.config.CopilotClientID
		return m, func() tea.Msg {
			dc, err := startCopilotDeviceAuth(context.Background(), clientID)
			if err != nil {
				return providerAuthFailedMsg{providerID: provider.ID, err: err}
			}
			return providerAuthStartedMsg{
				providerID: provider.ID,
				verifyURL:  dc.VerificationURI,
				userCode:   dc.UserCode,
				flow:       dc,
			}
		}
	default:
		return m, nil
	}
}

func (m ChatModel) saveProviderKey() (tea.Model, tea.Cmd) {
	if m.providersCursor < 0 || m.providersCursor >= len(m.providersList) {
		return m, nil
	}
	key := strings.TrimSpace(m.providerKeyInput)
	if key == "" {
		if m.providerAuthProvider == "claude" {
			m.providerStatus = "paste the callback URL or authorization code"
		} else {
			m.providerStatus = "missing API key"
		}
		return m, nil
	}
	if m.providerAuthProvider == "claude" {
		flow, _ := m.providerAuthFlow.(*claudeauth.Flow)
		return m, func() tea.Msg {
			session, err := exchangeClaudeAuth(context.Background(), flow, key)
			if err != nil {
				return providerAuthFailedMsg{providerID: "claude", err: err}
			}
			return providerAuthSucceededMsg{providerID: "claude", claudeSession: &session}
		}
	}
	tokens, err := auth.Load()
	if err != nil {
		m.providerStatus = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	setProviderToken(tokens, m.providersList[m.providersCursor].ID, key)
	if err := auth.Save(tokens); err != nil {
		m.providerStatus = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	m.providerPromptingKey = false
	m.providerPromptLabel = ""
	m.providerPromptMasked = false
	m.providerStatus = "saved"
	if m.config.RefreshProviders != nil {
		m.providersList = append([]ProviderOption(nil), m.config.RefreshProviders()...)
	}
	m.refreshModelList()
	provider := m.providersList[m.providersCursor]
	if provider.DefaultModel != "" && m.config.SwitchModel != nil {
		if newModel, err := m.config.SwitchModel(provider.DefaultModel); err == nil {
			m.model = newModel
			m.resetProviderDiagnostics()
			m.syncStatusData()
			m.flash = fmt.Sprintf("saved key and switched to %s", newModel)
			return m, nil
		} else {
			m.flash = "saved key"
		}
	} else {
		m.flash = "saved key"
	}
	return m, nil
}

func (m ChatModel) handleProviderAuthStarted(msg providerAuthStartedMsg) (tea.Model, tea.Cmd) {
	switch flow := msg.flow.(type) {
	case *claudeauth.Flow:
		m.providerAuthWaiting = false
		m.providerAuthProvider = msg.providerID
		m.providerAuthURL = msg.verifyURL
		m.providerAuthCode = msg.userCode
		m.providerAuthFlow = flow
		m.providerPromptingKey = true
		m.providerPromptLabel = "Paste callback/code"
		m.providerPromptMasked = false
		m.providerKeyInput = ""
		m.providerKeyPos = 0
		m.providerStatus = "open the browser URL, finish sign-in, then paste the callback URL or authorization code"
		return m, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.providerAuthCancel = cancel
	m.providerAuthWaiting = true
	m.providerAuthProvider = msg.providerID
	m.providerAuthURL = msg.verifyURL
	m.providerAuthCode = msg.userCode
	m.providerAuthFlow = msg.flow
	m.providerStatus = fmt.Sprintf("visit %s and enter %s", msg.verifyURL, msg.userCode)

	switch flow := msg.flow.(type) {
	case *chatgptauth.DeviceFlow:
		return m, func() tea.Msg {
			session, err := waitChatGPTDeviceAuth(ctx, flow)
			if err != nil {
				return providerAuthFailedMsg{providerID: msg.providerID, err: err}
			}
			return providerAuthSucceededMsg{providerID: msg.providerID, session: &session}
		}
	case *copilot.DeviceCode:
		clientID := m.config.CopilotClientID
		return m, func() tea.Msg {
			token, err := waitCopilotDeviceAuth(ctx, clientID, flow)
			if err != nil {
				return providerAuthFailedMsg{providerID: msg.providerID, err: err}
			}
			return providerAuthSucceededMsg{providerID: msg.providerID, token: token}
		}
	default:
		return m, nil
	}
}

func (m ChatModel) handleProviderAuthSucceeded(msg providerAuthSucceededMsg) (tea.Model, tea.Cmd) {
	if m.providerAuthCancel != nil {
		m.providerAuthCancel()
		m.providerAuthCancel = nil
	}
	tokens, err := auth.Load()
	if err != nil {
		m.providerAuthWaiting = false
		m.providerStatus = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	switch strings.ToLower(strings.TrimSpace(msg.providerID)) {
	case "chatgpt":
		if msg.session == nil {
			m.providerAuthWaiting = false
			m.providerStatus = "save failed: missing ChatGPT session"
			return m, nil
		}
		tokens = chatgptauth.StoreSession(tokens, *msg.session)
	case "claude":
		if msg.claudeSession == nil {
			m.providerPromptingKey = true
			m.providerStatus = "save failed: missing Claude session"
			return m, nil
		}
		tokens = claudeauth.StoreSession(tokens, *msg.claudeSession)
	case "copilot":
		tokens.CopilotToken = strings.TrimSpace(msg.token)
	}
	if err := auth.Save(tokens); err != nil {
		m.providerAuthWaiting = false
		m.providerStatus = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	m.providerAuthWaiting = false
	m.providerPromptingKey = false
	m.providerPromptLabel = ""
	m.providerPromptMasked = false
	m.providerKeyInput = ""
	m.providerKeyPos = 0
	m.providerAuthURL = ""
	m.providerAuthCode = ""
	m.providerAuthProvider = ""
	m.providerAuthFlow = nil
	m.providerStatus = "authenticated"
	if m.config.RefreshProviders != nil {
		m.providersList = append([]ProviderOption(nil), m.config.RefreshProviders()...)
	}
	m.refreshModelList()
	if m.providersCursor >= 0 && m.providersCursor < len(m.providersList) {
		provider := m.providersList[m.providersCursor]
		if provider.DefaultModel != "" && m.config.SwitchModel != nil {
			if newModel, err := m.config.SwitchModel(provider.DefaultModel); err == nil {
				m.model = newModel
				m.resetProviderDiagnostics()
				m.syncStatusData()
				m.flash = fmt.Sprintf("authenticated and switched to %s", newModel)
				return m, nil
			} else {
				m.flash = "authenticated"
			}
		} else {
			m.flash = "authenticated"
		}
	} else {
		m.flash = "authenticated"
	}
	return m, nil
}

func (m ChatModel) handleProviderAuthFailed(msg providerAuthFailedMsg) (tea.Model, tea.Cmd) {
	if m.providerAuthCancel != nil {
		m.providerAuthCancel = nil
	}
	m.providerAuthWaiting = false
	m.providerPromptingKey = false
	m.providerPromptLabel = ""
	m.providerPromptMasked = false
	m.providerAuthURL = ""
	m.providerAuthCode = ""
	m.providerAuthProvider = ""
	m.providerAuthFlow = nil
	if msg.err != nil {
		m.providerStatus = fmt.Sprintf("sign-in failed: %v", msg.err)
		m.flash = fmt.Sprintf("sign-in failed: %v", msg.err)
	} else {
		m.providerStatus = "sign-in failed"
		m.flash = "sign-in failed"
	}
	return m, nil
}

func (m ChatModel) deleteProviderCredential() (tea.Model, tea.Cmd) {
	if m.providersCursor < 0 || m.providersCursor >= len(m.providersList) {
		return m, nil
	}
	provider := m.providersList[m.providersCursor]
	tokens, err := auth.Load()
	if err != nil {
		m.providerStatus = fmt.Sprintf("delete failed: %v", err)
		return m, nil
	}
	if !providerHasStoredCredential(tokens, provider.ID) {
		m.providerStatus = "no stored credential"
		return m, nil
	}
	clearProviderToken(tokens, provider.ID)
	if err := auth.SaveExact(tokens); err != nil {
		m.providerStatus = fmt.Sprintf("delete failed: %v", err)
		return m, nil
	}
	if m.config.RefreshProviders != nil {
		m.providersList = append([]ProviderOption(nil), m.config.RefreshProviders()...)
	}
	m.refreshModelList()
	m.providerStatus = "deleted"
	m.flash = fmt.Sprintf("deleted %s key", provider.Label)
	return m, nil
}

func (m ChatModel) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	totalTabs := len(m.helpTabs())
	pageSize := max(1, m.height-10)
	maxScroll := max(0, len(m.helpLines())-pageSize)

	switch msg.Type {
	case tea.KeyEscape, tea.KeyEnter:
		m.helpVisible = false
	case tea.KeyLeft:
		m.helpTab = (m.helpTab + totalTabs - 1) % totalTabs
		m.helpScroll = 0
	case tea.KeyRight:
		m.helpTab = (m.helpTab + 1) % totalTabs
		m.helpScroll = 0
	case tea.KeyUp:
		m.helpScroll = max(0, m.helpScroll-1)
	case tea.KeyDown:
		m.helpScroll = min(maxScroll, m.helpScroll+1)
	case tea.KeyPgUp:
		m.helpScroll = max(0, m.helpScroll-pageSize)
	case tea.KeyPgDown:
		m.helpScroll = min(maxScroll, m.helpScroll+pageSize)
	case tea.KeyHome:
		m.helpScroll = 0
	case tea.KeyEnd:
		m.helpScroll = maxScroll
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case '?', 'q', 'Q':
				m.helpVisible = false
			case '[', 'h', 'H':
				m.helpTab = (m.helpTab + totalTabs - 1) % totalTabs
				m.helpScroll = 0
			case ']', 'l', 'L':
				m.helpTab = (m.helpTab + 1) % totalTabs
				m.helpScroll = 0
			case 'j', 'J':
				m.helpScroll = min(maxScroll, m.helpScroll+1)
			case 'k', 'K':
				m.helpScroll = max(0, m.helpScroll-1)
			case 'g':
				m.helpScroll = 0
			case 'G':
				m.helpScroll = maxScroll
			}
		}
	}
	return m, nil
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

func (m ChatModel) renderStatsOverlay() string {
	return renderStatsOverlayPanel(m.theme(), m.statsSnapshot(), m.width, m.height)
}

func (m ChatModel) renderTraceOverlay() string {
	return renderTraceOverlayPanel(m.theme(), m.renderedToolsBuf(), m.config.DebugLogPath, m.width, m.height)
}

func (m ChatModel) renderSearchOverlay() string {
	theme := m.theme()
	boxW := min(72, max(42, m.width-10))
	boxH := 7

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	inputStyle := lipgloss.NewStyle().Foreground(theme.Text).Background(theme.PanelBG)

	paneName := "agent"
	if m.searchPane == focusTools {
		paneName = "tools"
	}
	status := fmt.Sprintf("%d matches", len(m.searchMatches))
	if len(m.searchMatches) == 1 {
		status = "1 match"
	}
	if len(m.searchMatches) == 0 && strings.TrimSpace(m.searchQuery) != "" {
		status = "No matches"
	}
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Search"),
		dimStyle.Render("Pane: "+paneName),
		inputStyle.Render("Query: "+m.searchQuery),
		textStyle.Render(status),
		dimStyle.Render("Enter jump • Esc close"),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderFilesOverlay() string {
	theme := m.theme()
	boxW := min(96, max(42, m.width-6))
	boxH := min(30, max(12, m.height-4))
	contentHeight := max(1, boxH-8)

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(theme.HeaderFG).Background(theme.AccentPrimary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	inputStyle := lipgloss.NewStyle().Foreground(theme.Text).Background(theme.PanelBG)
	if m.filesViewing {
		return m.renderFileViewerOverlay(boxW, boxH, contentHeight, titleStyle, textStyle, dimStyle)
	}

	lines := make([]string, 0, min(len(m.filesFiltered), contentHeight))
	start := 0
	if m.filesCursor >= contentHeight {
		start = m.filesCursor - contentHeight + 1
	}
	for i := 0; i < contentHeight && start+i < len(m.filesFiltered); i++ {
		idx := start + i
		line := m.filesFiltered[idx]
		if idx == m.filesCursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, textStyle.Render(line))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("No matching files"))
	}
	title := "Add context file (@...)"
	footer := "Type to filter • Enter insert • Esc close"
	if m.filesBrowser {
		title = "Browse workspace files"
		footer = "Type to filter • Enter view • @ add context • Esc close"
	}
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render(title),
		inputStyle.Render("Query: "+m.filesQuery),
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(strings.Join(lines, "\n")),
		"",
		dimStyle.Render(footer),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderFileViewerOverlay(boxW, boxH, contentHeight int, titleStyle, textStyle, dimStyle lipgloss.Style) string {
	lines := strings.Split(m.filesViewText, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	m.filesViewScroll = clamp(m.filesViewScroll, 0, max(0, len(lines)-contentHeight))
	start := m.filesViewScroll
	end := min(len(lines), start+contentHeight)
	visible := make([]string, 0, contentHeight)
	lineNoWidth := len(fmt.Sprintf("%d", len(lines)))
	bodyW := max(20, boxW-10)
	for i := start; i < end; i++ {
		prefix := fmt.Sprintf("%*d ", lineNoWidth, i+1)
		line := prefix + lines[i]
		visible = append(visible, textStyle.Render(truncate(line, bodyW-1)))
	}
	for len(visible) < contentHeight {
		visible = append(visible, "")
	}
	for i := range visible {
		plainWidth := ansiPrintableWidth(visible[i])
		if plainWidth < bodyW {
			visible[i] += strings.Repeat(" ", bodyW-plainWidth)
		}
	}
	body := strings.Join(visible, "\n")
	footer := fmt.Sprintf("lines %d-%d/%d • j/k scroll • PgUp/PgDn • a/@ add context • q back • Esc close", min(len(lines), start+1), end, len(lines))
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("File preview"),
		dimStyle.Render(m.filesViewPath),
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(body),
		"",
		dimStyle.Render(truncate(footer, max(20, boxW-8))),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme().BorderFocus).
		Background(m.theme().HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderSessionRenameOverlay() string {
	theme := m.theme()
	boxW := min(64, max(38, m.width-10))
	boxH := 7
	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Rename session"),
		textStyle.Render("name> "+m.sessionRenameBuf),
		"",
		dimStyle.Render("Enter save • Esc cancel"),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderSessionsOverlay() string {
	theme := m.theme()
	boxW := min(88, max(56, m.width-6))
	boxH := min(28, max(12, m.height-4))
	contentHeight := max(1, boxH-6)

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(theme.HeaderFG).Background(theme.AccentPrimary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)

	lines := make([]string, 0, min(len(m.sessionsList), contentHeight))
	start := 0
	if m.sessionsCursor >= contentHeight {
		start = m.sessionsCursor - contentHeight + 1
	}
	for i := 0; i < contentHeight && start+i < len(m.sessionsList); i++ {
		idx := start + i
		entry := m.sessionsList[idx]
		line := fmt.Sprintf("%d. %s  (%s)", idx+1, entry.name, formatSessionTimestamp(entry.modTime))
		if idx == m.sessionsCursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, textStyle.Render(line))
		}
	}
	footer := dimStyle.Render("Enter restore • r rename • d delete • 1-9 quick restore • Esc close")
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Sessions"),
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(strings.Join(lines, "\n")),
		"",
		footer,
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	if m.sessionRenaming {
		return m.renderSessionRenameOverlay()
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderHelpOverlay() string {
	theme := m.theme()
	boxW := min(108, max(72, m.width-6))
	boxH := min(32, max(20, m.height-4))
	contentHeight := max(1, boxH-7)
	lines := m.helpLines()
	maxScroll := max(0, len(lines)-contentHeight)
	if m.helpScroll > maxScroll {
		m.helpScroll = maxScroll
	}

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	activeTabStyle := lipgloss.NewStyle().Background(theme.AccentPrimary).Foreground(theme.HeaderFG).Bold(true).Padding(0, 1)
	inactiveTabStyle := lipgloss.NewStyle().Background(theme.HeaderBG).Foreground(theme.TextDim).Padding(0, 1)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)

	tabs := m.helpTabs()
	renderedTabs := make([]string, 0, len(tabs))
	for i, tab := range tabs {
		if i == m.helpTab {
			renderedTabs = append(renderedTabs, activeTabStyle.Render(tab))
		} else {
			renderedTabs = append(renderedTabs, inactiveTabStyle.Render(tab))
		}
	}

	visible := make([]string, 0, contentHeight)
	for i := 0; i < contentHeight; i++ {
		idx := m.helpScroll + i
		if idx >= len(lines) {
			break
		}
		visible = append(visible, textStyle.Render(lines[idx]))
	}
	content := strings.Join(visible, "\n")
	footer := fmt.Sprintf("Tab %d/%d • lines %d-%d/%d • ←/→ switch tabs • ↑/↓ scroll • Esc closes", m.helpTab+1, len(tabs), min(len(lines), m.helpScroll+1), min(len(lines), m.helpScroll+contentHeight), len(lines))

	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Help"),
		strings.Join(renderedTabs, " "),
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(content),
		"",
		dimStyle.Render(footer),
	)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderModelsOverlay() string {
	theme := m.theme()
	boxW := min(96, max(56, m.width-6))
	boxH := min(28, max(14, m.height-4))
	contentHeight := max(1, boxH-8)

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(theme.HeaderFG).Background(theme.AccentPrimary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	inputStyle := lipgloss.NewStyle().Foreground(theme.Text).Background(theme.PanelBG)

	lines := make([]string, 0, min(len(m.modelsFiltered), contentHeight))
	start := 0
	if m.modelsCursor >= contentHeight {
		start = m.modelsCursor - contentHeight + 1
	}
	for i := 0; i < contentHeight && start+i < len(m.modelsFiltered); i++ {
		idx := start + i
		line := fmt.Sprintf("%d. %s", idx+1, m.modelOptionLabel(m.modelsFiltered[idx]))
		if idx == m.modelsCursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, textStyle.Render(line))
		}
	}
	queryLine := inputStyle.Render("Query: " + m.modelsQuery)
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("No matching models"))
	}
	rangeText := "0 models"
	if len(m.modelsFiltered) > 0 {
		rangeText = fmt.Sprintf("%d-%d/%d", start+1, min(len(m.modelsFiltered), start+len(lines)), len(m.modelsFiltered))
	}
	footer := dimStyle.Render("Type to filter • ↑/↓ select • Enter choose • " + rangeText + " • Esc close")
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Models"),
		queryLine,
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(strings.Join(lines, "\n")),
		"",
		footer,
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderProvidersOverlay() string {
	theme := m.theme()
	boxW := min(96, max(64, m.width-6))
	boxH := min(30, max(14, m.height-4))
	contentHeight := max(1, boxH-9)

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(theme.HeaderFG).Background(theme.AccentPrimary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	inputStyle := lipgloss.NewStyle().Foreground(theme.Text).Background(theme.PanelBG)

	lines := make([]string, 0, min(len(m.providersList), contentHeight))
	start := 0
	if m.providersCursor >= contentHeight {
		start = m.providersCursor - contentHeight + 1
	}
	for i := 0; i < contentHeight && start+i < len(m.providersList); i++ {
		idx := start + i
		provider := m.providersList[idx]
		line := provider.Label
		if provider.Status != "" {
			line += " — " + provider.Status
		}
		if provider.DefaultModel != "" {
			line += " (" + provider.DefaultModel + ")"
		}
		if idx == m.providersCursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, textStyle.Render(line))
		}
	}

	keyLine := ""
	if m.providerPromptingKey {
		if m.providerPromptMasked {
			keyLine = m.providerPromptLabel + ": " + strings.Repeat("*", len([]rune(m.providerKeyInput)))
		} else {
			keyLine = m.providerPromptLabel + ": " + m.providerKeyInput
		}
	}
	footerText := "↑/↓ select • Enter configure/select • d delete credential • Esc close"
	if m.providerPromptingKey {
		if m.providerAuthProvider == "claude" {
			footerText = "Ctrl+O open browser • Enter submit pasted callback/code • Esc cancel"
		} else {
			footerText = "Enter save key • Esc cancel"
		}
	} else if m.providerAuthWaiting {
		footerText = "Complete sign-in in browser • Esc cancel"
	}
	authLines := []string{}
	authWidth := max(1, boxW-6)
	if m.providerAuthWaiting || (m.providerPromptingKey && m.providerAuthProvider == "claude" && m.providerAuthURL != "") {
		if m.providerAuthURL != "" {
			authLines = append(authLines, textStyle.Render("Open URL:"))
			authLines = append(authLines, textStyle.Render(providerAuthHyperlink("Open Claude sign-in page", m.providerAuthURL)))
		}
		if m.providerAuthCode != "" {
			authLines = append(authLines, textStyle.Render("Code:"))
			authLines = append(authLines, textStyle.Render(wrapProviderAuthValue(m.providerAuthCode, authWidth)))
		}
	}
	if m.providerPromptingKey && m.providerAuthProvider == "claude" {
		keyLine = inputStyle.Render(keyLine)
	} else if keyLine != "" {
		keyLine = textStyle.Render(keyLine)
	}
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Providers"),
		dimStyle.Render("Select a provider. API-key providers prompt for a key; ChatGPT/Claude/Copilot can sign in here."),
		"",
		lipgloss.NewStyle().Width(authWidth).Height(contentHeight).Render(strings.Join(lines, "\n")),
		"",
		lipgloss.NewStyle().Width(authWidth).Render(strings.Join(authLines, "\n")),
		keyLine,
		dimStyle.Render(m.providerStatus),
		dimStyle.Render(footerText),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) snapshot() chatSessionSnapshot {
	return chatSessionSnapshot{
		SavedAt:      time.Now(),
		Model:        m.model,
		WorkDir:      m.workDir,
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
		approvalStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.Warning).
			Background(theme.HeaderBG).
			Foreground(theme.Text).
			Width(m.width - 4)
		approvalText := fmt.Sprintf("Tool: %s\n%s\n\n[y]es / [n]o", m.pendingApproval.Tool, m.pendingApproval.Summary)
		inputBox = approvalStyle.Render(approvalText)
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

func (m ChatModel) renderLiveProgressSlot(theme chatTheme) string {
	message, busy := m.transientStatusMessage()
	slotStyle := lipgloss.NewStyle().
		Foreground(theme.TextDim).
		Width(m.width)
	if message == "" {
		return slotStyle.Render("")
	}
	prefix := "·"
	if busy {
		prefix = chatSpinnerGlyph(m.spinnerFrame)
		slotStyle = slotStyle.Foreground(theme.AccentPrimary).Bold(true)
	}
	return slotStyle.Render(fitCell(prefix+" "+message, max(1, m.width)))
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

func (m ChatModel) renderComposerGap(theme chatTheme) string {
	if m.width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().
		Width(m.width).
		Foreground(theme.TextDim).
		Render(strings.Repeat(" ", m.width))
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
	parts := make([]string, 0, 4)
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
	if m.lastRenderStats.Hits > 0 || m.lastRenderStats.Misses > 0 {
		cachePart := fmt.Sprintf("cache %d hits", m.lastRenderStats.Hits)
		if m.lastRenderStats.Misses > 0 {
			cachePart += fmt.Sprintf(" / %d misses", m.lastRenderStats.Misses)
		}
		// Only show misses when both hits and misses are zero is not possible here (guard above).
		// But when hits=0 and misses>0, we still want to show it:
		// Actually the guard already handles this: if Hits>0 || Misses>0
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
	if message := normalizeStatusMessage(m.liveProgress.LatestMessage()); message != "" {
		return message, m.busy
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
		Kind:    MsgStatus,
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
	command = strings.TrimSpace(command)
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
