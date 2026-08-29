package react

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"forge/internal/hooks"
	"forge/internal/llm"
	"forge/internal/protocol"
	"forge/internal/secscan"
	"forge/internal/sessionstore"
)

type TurnToolCall struct {
	Name string
}

type Mode string

type HookOverlay = hooks.Overlay
type HookPriority = hooks.Priority

const (
	ModeChat      Mode = "chat"
	ModeInspect   Mode = "inspect"
	ModePlan      Mode = "plan"
	ModeImplement Mode = "implement"
	ModeValidate  Mode = "validate"
	ModeReview    Mode = "review"
	ModePreview   Mode = "preview"

	HookPriorityLow    HookPriority = hooks.PriorityLow
	HookPriorityNormal HookPriority = hooks.PriorityNormal
	HookPriorityHigh   HookPriority = hooks.PriorityHigh
)

var ErrStaleTurn = errors.New("stale turn")

type TaskState struct {
	Objective            string
	RequiredVerification string
	Operation            string
	SourceRef            string
	TargetBranch         string
}

type AgentTaskActivity struct {
	ToolName string    `json:"tool_name"`
	Summary  string    `json:"summary,omitempty"`
	At       time.Time `json:"at"`
}

type AgentTaskState struct {
	ID             string              `json:"id"`
	Role           string              `json:"role"`
	Description    string              `json:"description,omitempty"`
	Prompt         string              `json:"prompt,omitempty"`
	Status         AgentStatus         `json:"status"`
	CreatedAt      time.Time           `json:"created_at"`
	StartedAt      time.Time           `json:"started_at,omitempty"`
	CompletedAt    time.Time           `json:"completed_at,omitempty"`
	LastActivityAt time.Time           `json:"last_activity_at,omitempty"`
	Result         string              `json:"result,omitempty"`
	Error          string              `json:"error,omitempty"`
	Handoff        *AgentHandoff       `json:"handoff,omitempty"`
	ParentTurn     int                 `json:"parent_turn,omitempty"`
	LastToolName   string              `json:"last_tool_name,omitempty"`
	RecentActivity []AgentTaskActivity `json:"recent_activity,omitempty"`
}

type PlanStep struct {
	Step    string `json:"step"`
	Status  string `json:"status"`
	Blocker string `json:"blocker,omitempty"`
}

type PlanState struct {
	Explanation string     `json:"explanation,omitempty"`
	Steps       []PlanStep `json:"steps"`
}

func (s PlanState) ActiveStep() (PlanStep, bool) {
	for _, step := range s.Steps {
		switch strings.ToLower(strings.TrimSpace(step.Status)) {
		case "complete", "completed", "done", "success", "succeeded", "skipped", "cancelled", "canceled":
			continue
		default:
			return step, true
		}
	}
	return PlanStep{}, false
}

func (s PlanState) HasActiveStep() bool {
	_, ok := s.ActiveStep()
	return ok
}

func (s PlanState) BlockedStep() (PlanStep, bool) {
	for _, step := range s.Steps {
		if strings.EqualFold(strings.TrimSpace(step.Status), "blocked") {
			return step, true
		}
	}
	return PlanStep{}, false
}

type TurnRecord struct {
	Number        int
	Input         string
	FinalResponse string
	ToolCalls     []TurnToolCall
	Error         string
}

type TurnPhase string

const (
	TurnPhaseCreated         TurnPhase = "created"
	TurnPhaseRunningModel    TurnPhase = "running_model"
	TurnPhaseRunningTools    TurnPhase = "running_tools"
	TurnPhaseWaitingApproval TurnPhase = "waiting_approval"
	TurnPhaseValidating      TurnPhase = "validating"
)

type TurnEndReason string

const (
	TurnEndReasonCompleted TurnEndReason = "completed"
	TurnEndReasonCancelled TurnEndReason = "cancelled"
	TurnEndReasonFailed    TurnEndReason = "failed"
)

type ActiveTurn struct {
	ID           string
	Number       int
	Phase        TurnPhase
	Context      context.Context
	CancelReason string
	cancel       context.CancelFunc
}

type SessionSnapshot struct {
	Turn                 int
	LastInput            string
	InitialInput         string
	RecentInputs         []string
	History              []llm.Message
	Turns                []TurnRecord
	Items                []protocol.Item
	CompactedTurns       int
	CompactionSummary    string
	MemorySummary        string
	HookOutputSet        bool
	HookOutput           hooks.ExecutionOutput
	HookOverlays         []hooks.Overlay
	RuntimeNote          string
	Mode                 Mode
	TaskState            *TaskState
	PlanState            *PlanState
	AgentTasks           []AgentTaskState
	PendingInput         []string
	Interrupted          bool
	LastDurableError     string
	LastTurnEndReason    TurnEndReason
	LastTurnCancelReason string
	ActiveWorkspaceRoot  string
	// ContextWindowTokens lets prompt building scale its truncation limits to
	// the active model. Zero means unknown, and the flat defaults apply.
	ContextWindowTokens int
}

type Session struct {
	mu              sync.Mutex
	contextWindowFn func() int
	turn            int
	lastInput       string
	initialInput    string
	recentInputs    []string
	// items is the only source of conversation truth. historyCache is a
	// projection of it, rebuilt on demand; nothing may write to it directly.
	// Keeping a second, independently mutated message list is what let the
	// live prompt and the replayed prompt drift apart.
	historyCache         []llm.Message
	historyDirty         bool
	turns                []TurnRecord
	items                []protocol.Item
	refSeq               int64
	compactedTurns       int
	compactionSummary    string
	memorySummary        string
	hookOutputSet        bool
	hookOutput           hooks.ExecutionOutput
	hookOverlays         []hooks.Overlay
	runtimeNote          string
	mode                 Mode
	taskState            *TaskState
	planState            *PlanState
	agentTasks           map[string]AgentTaskState
	agentTaskOrder       []string
	pendingInput         []string
	interrupted          bool
	lastTurnEndReason    TurnEndReason
	lastTurnCancelReason string
	activeWorkspaceRoot  string
	durableSink          DurableSink
	durableThreadID      string
	lastDurableError     string
	activeTurn           *ActiveTurn
}

type DurableSink interface {
	Append(context.Context, protocol.Item) error
}

func NewSession() *Session {
	return &Session{mode: ModeChat}
}

func turnNumberFromID(turnID string) int {
	trimmed := strings.TrimSpace(strings.TrimPrefix(turnID, "turn-"))
	if trimmed == "" || trimmed == turnID {
		return 0
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (s *Session) SetDurableSink(sink DurableSink) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.durableSink = sink
}

func (s *Session) SetDurableThreadID(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.durableThreadID = id
}

func (s *Session) DurableThreadID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.durableThreadID
}

// AdoptReplayItems replaces this session's conversation state with the history
// replayed from a previously persisted thread, so a restored chat continues
// with full model context. Turn bookkeeping and durable persistence of the
// current thread are left untouched; new turns append to the current thread.
func (s *Session) AdoptReplayItems(items []protocol.Item) (int, error) {
	if s == nil {
		return 0, nil
	}
	replay, err := sessionstore.ReplayItems(items)
	if err != nil {
		return 0, err
	}
	// Adopt the resolved log itself, so the resumed session derives its prompt
	// the same way the live one does. Any compaction in the adopted thread is
	// already folded in, and sequences are renumbered onto this session.
	resolved := sessionstore.ResolveItems(items)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = nil
	s.historyCache = nil
	s.historyDirty = true
	s.refSeq = 0
	for _, item := range resolved {
		item.Seq = 0
		item.ID = ""
		item.Ref = ""
		s.appendItemLocked(item)
	}
	s.recentInputs = append([]string(nil), replay.RecentInputs...)
	s.compactionSummary = replay.CompactionSummary
	if len(s.recentInputs) > 0 {
		s.initialInput = strings.TrimSpace(s.recentInputs[0])
		s.lastInput = strings.TrimSpace(s.recentInputs[len(s.recentInputs)-1])
	}
	return len(s.historyLocked()), nil
}

func (s *Session) DurableSink() DurableSink {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.durableSink
}

func (s *Session) BeginTurn(parent context.Context, turnID string) (ActiveTurn, context.CancelFunc, error) {
	if s == nil {
		return ActiveTurn{}, nil, fmt.Errorf("react session: session is nil")
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return ActiveTurn{}, nil, fmt.Errorf("react session: turn ID is empty")
	}
	if parent == nil {
		parent = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTurn != nil {
		return ActiveTurn{}, nil, fmt.Errorf("react session: active turn %q overlaps %q", s.activeTurn.ID, turnID)
	}
	ctx, cancel := context.WithCancel(parent)
	active := ActiveTurn{
		ID:      turnID,
		Number:  turnNumberFromID(turnID),
		Phase:   TurnPhaseCreated,
		Context: ctx,
		cancel:  cancel,
	}
	s.activeTurn = &active
	return active, cancel, nil
}

func (s *Session) persistDurableItem(item protocol.Item, sink DurableSink) error {
	if sink == nil {
		s.recordDurableAppendResult(nil)
		return nil
	}
	err := appendDurableItem(sink, item)
	s.recordDurableAppendResult(err)
	return err
}

func (s *Session) persistDurableItems(items []protocol.Item, sink DurableSink) error {
	for _, item := range items {
		if err := s.persistDurableItem(item, sink); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) SetActiveTurnPhase(turnID string, phase TurnPhase) error {
	if s == nil {
		return fmt.Errorf("react session: session is nil")
	}
	turnID = strings.TrimSpace(turnID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTurn == nil {
		return fmt.Errorf("react session: no active turn")
	}
	if s.activeTurn.ID != turnID {
		return fmt.Errorf("react session: active turn %q does not match %q", s.activeTurn.ID, turnID)
	}
	s.activeTurn.Phase = phase
	return nil
}

func (s *Session) EndTurn(turnID string, reason TurnEndReason) error {
	if s == nil {
		return fmt.Errorf("react session: session is nil")
	}
	turnID = strings.TrimSpace(turnID)
	s.mu.Lock()
	if s.activeTurn == nil {
		s.mu.Unlock()
		return fmt.Errorf("react session: no active turn")
	}
	if s.activeTurn.ID != turnID {
		activeID := s.activeTurn.ID
		s.mu.Unlock()
		return fmt.Errorf("react session: active turn %q does not match %q", activeID, turnID)
	}
	cancel := s.activeTurn.cancel
	s.lastTurnEndReason = reason
	s.lastTurnCancelReason = s.activeTurn.CancelReason
	s.activeTurn = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *Session) CancelActiveTurn(reason string) error {
	if s == nil {
		return fmt.Errorf("react session: session is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTurn == nil {
		return fmt.Errorf("react session: no active turn")
	}
	if s.activeTurn.cancel != nil {
		s.activeTurn.cancel()
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		s.activeTurn.CancelReason = reason
		s.lastTurnCancelReason = reason
		s.interrupted = true
	}
	return nil
}

func (s *Session) ActiveTurnSnapshot() (ActiveTurn, bool) {
	if s == nil {
		return ActiveTurn{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTurn == nil {
		return ActiveTurn{}, false
	}
	return *s.activeTurn, true
}

func (s *Session) IsActiveTurn(turnID string) bool {
	if s == nil {
		return false
	}
	turnID = strings.TrimSpace(turnID)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeTurnMatchesLocked(turnID)
}

func (s *Session) activeTurnMatchesLocked(turnID string) bool {
	if s.activeTurn == nil || s.activeTurn.ID != turnID {
		return false
	}
	return s.activeTurn.Context == nil || s.activeTurn.Context.Err() == nil
}

func staleTurnError(turnID string) error {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return fmt.Errorf("%w: empty turn ID", ErrStaleTurn)
	}
	return fmt.Errorf("%w: %s", ErrStaleTurn, turnID)
}

func (s *Session) AppendToolResultForTurn(turnID string, result protocol.ToolResultItem) error {
	if s == nil {
		return fmt.Errorf("%w: session is nil", ErrStaleTurn)
	}
	turnID = strings.TrimSpace(turnID)
	s.mu.Lock()
	if !s.activeTurnMatchesLocked(turnID) {
		s.mu.Unlock()
		return staleTurnError(turnID)
	}
	if strings.TrimSpace(result.ToolCallID) == "" {
		s.mu.Unlock()
		return nil
	}
	item := s.appendItemLocked(protocol.Item{
		Kind:       protocol.ItemToolResult,
		TurnID:     turnID,
		ToolResult: &result,
	})
	sink := s.durableSink
	s.mu.Unlock()
	return s.persistDurableItem(item, sink)
}

func (s *Session) AppendFailureForTurn(turnID string, failure protocol.FailureItem) error {
	if s == nil {
		return fmt.Errorf("%w: session is nil", ErrStaleTurn)
	}
	turnID = strings.TrimSpace(turnID)
	s.mu.Lock()
	if !s.activeTurnMatchesLocked(turnID) {
		s.mu.Unlock()
		return staleTurnError(turnID)
	}
	item := s.appendItemLocked(protocol.Item{
		Kind:    protocol.ItemFailure,
		TurnID:  turnID,
		Failure: &failure,
	})
	sink := s.durableSink
	s.mu.Unlock()
	return s.persistDurableItem(item, sink)
}

func (s *Session) AppendToolCallForTurn(turnID string, toolCall protocol.ToolCallItem) error {
	if s == nil {
		return fmt.Errorf("%w: session is nil", ErrStaleTurn)
	}
	turnID = strings.TrimSpace(turnID)
	s.mu.Lock()
	if !s.activeTurnMatchesLocked(turnID) {
		s.mu.Unlock()
		return staleTurnError(turnID)
	}
	if callID := strings.TrimSpace(toolCall.ToolCallID); callID != "" {
		for _, item := range s.items {
			if item.TurnID == turnID && item.ToolCall != nil && item.ToolCall.ToolCallID == callID {
				s.mu.Unlock()
				return nil
			}
		}
	}
	item := s.appendItemLocked(protocol.Item{
		Kind:     protocol.ItemToolCall,
		TurnID:   turnID,
		ToolCall: &toolCall,
	})
	sink := s.durableSink
	s.mu.Unlock()
	return s.persistDurableItem(item, sink)
}

func (s *Session) AppendFailureAndToolResultForTurn(turnID string, failure protocol.FailureItem, toolCall protocol.ToolCallItem, result protocol.ToolResultItem) error {
	if s == nil {
		return fmt.Errorf("%w: session is nil", ErrStaleTurn)
	}
	turnID = strings.TrimSpace(turnID)
	s.mu.Lock()
	if !s.activeTurnMatchesLocked(turnID) {
		s.mu.Unlock()
		return staleTurnError(turnID)
	}
	if strings.TrimSpace(result.ToolCallID) == "" {
		s.mu.Unlock()
		return nil
	}
	failureItem := s.appendItemLocked(protocol.Item{
		Kind:     protocol.ItemFailure,
		TurnID:   turnID,
		Failure:  &failure,
		ToolCall: &toolCall,
	})
	resultItem := s.appendItemLocked(protocol.Item{
		Kind:       protocol.ItemToolResult,
		TurnID:     turnID,
		ToolResult: &result,
	})
	sink := s.durableSink
	s.mu.Unlock()
	if err := s.persistDurableItem(failureItem, sink); err != nil {
		return err
	}
	return s.persistDurableItem(resultItem, sink)
}

func (s *Session) AppendItem(item protocol.Item) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	item = s.appendItemLocked(item)
	sink := s.durableSink
	s.mu.Unlock()
	return s.persistDurableItem(item, sink)
}

func (s *Session) appendItemLocked(item protocol.Item) protocol.Item {
	if item.Version == 0 {
		item.Version = protocol.CurrentItemVersion
	}
	if item.ThreadID == "" {
		item.ThreadID = "session"
	}
	if item.TurnID == "" && s.turn > 0 {
		item.TurnID = fmt.Sprintf("turn-%d", s.turn)
	}
	if item.Seq == 0 {
		item.Seq = int64(len(s.items) + 1)
	}
	if item.ID == "" {
		item.ID = fmt.Sprintf("item-%d", len(s.items)+1)
	}
	if item.Ref == "" {
		s.refSeq++
		item.Ref = fmt.Sprintf("ref-%d", s.refSeq)
	}
	if item.At.IsZero() {
		item.At = time.Now().UTC()
	}
	s.items = append(s.items, item)
	s.historyDirty = true
	return item
}

// historyLocked projects the item log into the message list sent to the model.
func (s *Session) historyLocked() []llm.Message {
	if !s.historyDirty && s.historyCache != nil {
		return s.historyCache
	}
	replay, err := sessionstore.ReplayItems(s.items)
	if err != nil {
		// A projection failure must not silently serve a stale prompt.
		s.lastDurableError = err.Error()
		return s.historyCache
	}
	s.historyCache = replay.History
	s.historyDirty = false
	return s.historyCache
}

// invalidateHistoryLocked forces the next projection to rebuild.
func (s *Session) invalidateHistoryLocked() {
	s.historyDirty = true
}

func (s *Session) recordDurableAppendResult(err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.lastDurableError = strings.TrimSpace(err.Error())
		return
	}
	s.lastDurableError = ""
}

func (s *Session) RecordDurableError(err error) {
	if err == nil {
		return
	}
	s.recordDurableAppendResult(err)
}

func (s *Session) RecordInput(input string) int {
	turn, _ := s.RecordInputWithParts(input, nil)
	return turn
}

func (s *Session) AppendSkillContext(name, body string) {
	if s == nil {
		return
	}
	name = strings.TrimSpace(name)
	body = strings.TrimSpace(body)
	if name == "" && body == "" {
		return
	}
	s.mu.Lock()
	item := s.appendItemLocked(protocol.Item{
		Kind:         protocol.ItemSkillContext,
		SkillContext: &protocol.SkillContextItem{Name: name, Body: body},
	})
	sink := s.durableSink
	s.mu.Unlock()
	_ = s.persistDurableItem(item, sink)
}

func (s *Session) RecordInputWithParts(input string, parts []llm.MessageContentPart) (int, error) {
	if s == nil {
		return 0, nil
	}
	s.mu.Lock()
	turn, item := s.recordInputWithPartsLocked(input, parts)
	sink := s.durableSink
	s.mu.Unlock()
	return turn, s.persistDurableItem(item, sink)
}

func (s *Session) recordInputWithPartsLocked(input string, parts []llm.MessageContentPart) (int, protocol.Item) {
	s.turn++
	turn := s.turn
	s.lastInput = input
	if strings.TrimSpace(s.initialInput) == "" {
		s.initialInput = strings.TrimSpace(input)
	}
	s.recentInputs = append(s.recentInputs, input)
	msg := llm.Message{Role: llm.RoleUser, Content: input}
	if len(parts) > 0 {
		msg.ContentParts = parts
	}
	s.turns = append(s.turns, TurnRecord{
		Number: turn,
		Input:  input,
	})
	item := s.appendItemLocked(protocol.Item{
		Kind:   protocol.ItemUserMessage,
		TurnID: fmt.Sprintf("turn-%d", turn),
		Message: &protocol.MessageItem{
			Role:         string(llm.RoleUser),
			Text:         input,
			ContentParts: parts,
		},
	})
	return turn, item
}

func (s *Session) CompleteTurn(turn int, response string, toolCalls []TurnToolCall, err error) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	for i := range s.turns {
		if s.turns[i].Number != turn {
			continue
		}
		if strings.TrimSpace(response) != "" {
			s.turns[i].FinalResponse = strings.TrimSpace(response)
		}
		if toolCalls != nil {
			s.turns[i].ToolCalls = append([]TurnToolCall(nil), toolCalls...)
		}
		turnID := fmt.Sprintf("turn-%d", turn)
		if s.turnHasTerminalLocked(turnID) {
			s.mu.Unlock()
			return nil
		}
		if err != nil {
			s.turns[i].Error = strings.TrimSpace(err.Error())
		}
		var item protocol.Item
		if err != nil {
			item = s.appendItemLocked(protocol.Item{
				Kind:    protocol.ItemFailure,
				TurnID:  turnID,
				Failure: &protocol.FailureItem{Decision: classifyTurnFailure(err)},
			})
		} else {
			item = s.appendItemLocked(protocol.Item{
				Kind:         protocol.ItemTurnComplete,
				TurnID:       turnID,
				TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted},
			})
		}
		sink := s.durableSink
		s.interrupted = false
		s.mu.Unlock()
		return s.persistDurableItem(item, sink)
	}
	s.mu.Unlock()
	return nil
}

func classifyTurnFailure(err error) protocol.FailureDecision {
	if err == nil {
		return protocol.FailureDecision{Class: protocol.FailureNone}
	}
	if _, ok := errors.AsType[*RetryableCompletionError](err); ok {
		return protocol.ClassifyModelOutputFailure("error: " + err.Error())
	}
	return protocol.ClassifyToolExecutionFailure("runtime", err)
}

func (s *Session) AppendAssistantMessage(text string) error {
	if s == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	s.mu.Lock()
	trimmed := strings.TrimSpace(text)
	item := s.appendItemLocked(protocol.Item{
		Kind:    protocol.ItemAssistantMessage,
		Message: &protocol.MessageItem{Role: string(llm.RoleAssistant), Text: trimmed},
	})
	sink := s.durableSink
	s.mu.Unlock()
	return s.persistDurableItem(item, sink)
}

func (s *Session) AppendUserMessage(text string) error {
	if s == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	s.mu.Lock()
	trimmed := strings.TrimSpace(text)
	item := s.appendItemLocked(protocol.Item{
		Kind:    protocol.ItemUserMessage,
		Message: &protocol.MessageItem{Role: string(llm.RoleUser), Text: trimmed},
	})
	sink := s.durableSink
	s.mu.Unlock()
	return s.persistDurableItem(item, sink)
}

// AppendUserParts records multimodal content produced by a tool as a user
// message. Images cannot travel in a tool message -- OpenAI-compatible APIs
// reject them there -- so this is how a tool hands a vision model something to
// look at. Drivers that cannot take images degrade to a placeholder note.
func (s *Session) AppendUserParts(text string, parts []llm.MessageContentPart) error {
	if s == nil || len(parts) == 0 {
		return nil
	}
	s.mu.Lock()
	item := s.appendItemLocked(protocol.Item{
		Kind: protocol.ItemUserMessage,
		Message: &protocol.MessageItem{
			Role:         string(llm.RoleUser),
			Text:         strings.TrimSpace(text),
			ContentParts: parts,
		},
	})
	sink := s.durableSink
	s.mu.Unlock()
	return s.persistDurableItem(item, sink)
}

func (s *Session) appendQueuedUserInput(text string) {
	trimmed := strings.TrimSpace(text)
	if s == nil || trimmed == "" {
		return
	}
	s.mu.Lock()
	s.lastInput = trimmed
	s.recentInputs = append(s.recentInputs, trimmed)
	items := []protocol.Item{s.appendItemLocked(protocol.Item{
		Kind:    protocol.ItemUserMessage,
		Message: &protocol.MessageItem{Role: string(llm.RoleUser), Text: trimmed},
	}), s.appendItemLocked(protocol.Item{
		Kind:        protocol.ItemTurnContext,
		TurnContext: &protocol.TurnContextItem{Input: trimmed, Mode: "queued_input_consumed"},
	})}
	sink := s.durableSink
	s.mu.Unlock()
	_ = s.persistDurableItems(items, sink)
}

// AppendAssistantWithToolCalls records an assistant message that contains native
// tool calls (may have empty text content). Used by the native tool calling path.
func (s *Session) AppendAssistantWithToolCalls(calls []llm.NativeToolCall) error {
	return s.AppendAssistantToolTurn("", calls)
}

// AppendAssistantToolTurn records an assistant message that may include both a
// short natural-language preamble and native tool calls.
func (s *Session) AppendAssistantToolTurn(text string, calls []llm.NativeToolCall) error {
	if s == nil || len(calls) == 0 {
		return nil
	}
	s.mu.Lock()
	text = redactRuntimeText(strings.TrimSpace(text))
	if len(s.turns) > 0 {
		last := &s.turns[len(s.turns)-1]
		last.ToolCalls = make([]TurnToolCall, 0, len(calls))
		for _, call := range calls {
			last.ToolCalls = append(last.ToolCalls, TurnToolCall{Name: strings.TrimSpace(call.Name)})
		}
	}
	items := make([]protocol.Item, 0, len(calls)+1)
	// Recorded even when the preamble is empty: this is the message the tool
	// calls belong to, and where reasoning attaches.
	items = append(items, s.appendItemLocked(protocol.Item{
		Kind:    protocol.ItemAssistantMessage,
		Message: &protocol.MessageItem{Role: string(llm.RoleAssistant), Text: text},
	}))
	for _, call := range calls {
		items = append(items, s.appendItemLocked(nativeToolCallItem(call)))
	}
	sink := s.durableSink
	s.mu.Unlock()
	return s.persistDurableItems(items, sink)
}

func nativeToolCallItem(call llm.NativeToolCall) protocol.Item {
	item := protocol.Item{
		Kind: protocol.ItemToolCall,
		ToolCall: &protocol.ToolCallItem{
			ToolName:   strings.TrimSpace(call.Name),
			ToolCallID: strings.TrimSpace(call.ID),
		},
	}
	redacted := redactRuntimeText(strings.TrimSpace(call.ArgsJSON))
	if redacted == "" {
		return item
	}
	item.ToolCall.ArgsJSON = redacted
	var args map[string]any
	if err := json.Unmarshal([]byte(redacted), &args); err == nil {
		item.ToolCall.Args = args
	}
	return item
}

func redactNativeToolCalls(calls []llm.NativeToolCall) []llm.NativeToolCall {
	if len(calls) == 0 {
		return nil
	}
	scanner := secscan.NewDefaultScanner()
	redacted := make([]llm.NativeToolCall, 0, len(calls))
	for _, call := range calls {
		call.ArgsJSON = secscan.Redact(call.ArgsJSON, scanner.Scan(call.ArgsJSON))
		redacted = append(redacted, call)
	}
	return redacted
}

func (s *Session) SetLastAssistantReasoning(reasoning string) {
	if s == nil || reasoning == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	redacted := redactRuntimeText(reasoning)
	for i := len(s.items) - 1; i >= 0; i-- {
		item := s.items[i]
		if item.Kind == protocol.ItemAssistantMessage && item.Message != nil {
			clone := *item.Message
			clone.ReasoningContent = redacted
			s.items[i].Message = &clone
			s.invalidateHistoryLocked()
			return
		}
	}
}

// AppendNativeToolResult records a tool execution result matched to a specific
// tool call ID. Used by the native tool calling path.
func (s *Session) AppendNativeToolResult(toolCallID, result string) error {
	if s == nil || strings.TrimSpace(toolCallID) == "" {
		return nil
	}
	s.mu.Lock()
	toolName := s.toolNameForCallIDLocked(toolCallID)
	item := s.appendItemLocked(protocol.Item{
		Kind:       protocol.ItemToolResult,
		ToolResult: &protocol.ToolResultItem{ToolName: toolName, ToolCallID: toolCallID, Text: result},
	})
	sink := s.durableSink
	s.mu.Unlock()
	return s.persistDurableItem(item, sink)
}

func (s *Session) AppendStats(duration time.Duration, usage llm.Usage) {
	if s == nil {
		return
	}
	s.mu.Lock()
	item := s.appendItemLocked(protocol.Item{
		Kind:  protocol.ItemStats,
		Stats: &protocol.StatsItem{DurationMillis: duration.Milliseconds(), Usage: usage},
	})
	sink := s.durableSink
	s.mu.Unlock()
	_ = s.persistDurableItem(item, sink)
}

func (s *Session) toolNameForCallIDLocked(toolCallID string) string {
	for i := len(s.items) - 1; i >= 0; i-- {
		item := s.items[i]
		if item.ToolCall != nil && item.ToolCall.ToolCallID == toolCallID {
			return item.ToolCall.ToolName
		}
	}
	history := s.historyLocked()
	for i := len(history) - 1; i >= 0; i-- {
		for _, call := range history[i].ToolCalls {
			if call.ID == toolCallID {
				return call.Name
			}
		}
	}
	return ""
}

func appendDurableItem(sink DurableSink, item protocol.Item) error {
	if sink == nil {
		return nil
	}
	item.ThreadID = ""
	item.Seq = 0
	item.ID = ""
	// Ref is deliberately preserved: compaction records point at it.
	return sink.Append(context.Background(), item)
}

// SetContextWindowFn supplies the active model's window so prompt building can
// scale its truncation limits instead of applying flat defaults.
func (s *Session) SetContextWindowFn(fn func() int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.contextWindowFn = fn
	s.mu.Unlock()
}

func (s *Session) Messages(systemPrompt string) []llm.Message {
	return BuildMessages(systemPrompt, s.Snapshot())
}

func (s *Session) Snapshot() SessionSnapshot {
	if s == nil {
		return SessionSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	window := 0
	if s.contextWindowFn != nil {
		window = s.contextWindowFn()
	}
	return SessionSnapshot{
		ContextWindowTokens:  window,
		Turn:                 s.turn,
		LastInput:            s.lastInput,
		InitialInput:         s.initialInput,
		RecentInputs:         append([]string(nil), s.recentInputs...),
		History:              append([]llm.Message(nil), s.historyLocked()...),
		Turns:                append([]TurnRecord(nil), s.turns...),
		Items:                cloneProtocolItems(s.items),
		CompactedTurns:       s.compactedTurns,
		CompactionSummary:    s.compactionSummary,
		MemorySummary:        s.memorySummary,
		HookOutputSet:        s.hookOutputSet,
		HookOutput:           cloneHookOutput(s.hookOutput),
		HookOverlays:         append([]hooks.Overlay(nil), s.hookOverlays...),
		RuntimeNote:          s.runtimeNote,
		Mode:                 s.mode,
		TaskState:            cloneTaskState(s.taskState),
		PlanState:            clonePlanState(s.planState),
		AgentTasks:           cloneAgentTasksLocked(s.agentTaskOrder, s.agentTasks),
		PendingInput:         append([]string(nil), s.pendingInput...),
		Interrupted:          s.interrupted,
		LastDurableError:     s.lastDurableError,
		LastTurnEndReason:    s.lastTurnEndReason,
		LastTurnCancelReason: s.lastTurnCancelReason,
		ActiveWorkspaceRoot:  s.activeWorkspaceRoot,
	}
}

func cloneProtocolItems(items []protocol.Item) []protocol.Item {
	return append([]protocol.Item(nil), items...)
}

func (s *Session) SetActiveWorkspaceRoot(root string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeWorkspaceRoot = strings.TrimSpace(root)
}

func (s *Session) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turn = 0
	s.lastInput = ""
	s.initialInput = ""
	s.recentInputs = nil
	s.items = nil
	s.historyCache = nil
	s.historyDirty = true
	s.refSeq = 0
	s.turns = nil
	s.compactedTurns = 0
	s.compactionSummary = ""
	s.memorySummary = ""
	s.hookOutputSet = false
	s.hookOutput = hooks.ExecutionOutput{}
	s.hookOverlays = nil
	s.runtimeNote = ""
	s.mode = ModeChat
	s.taskState = nil
	s.planState = nil
	s.agentTasks = nil
	s.agentTaskOrder = nil
	s.pendingInput = nil
	s.interrupted = false
	s.lastDurableError = ""
	s.lastTurnEndReason = ""
	s.lastTurnCancelReason = ""
	s.activeWorkspaceRoot = ""
	s.activeTurn = nil
}

func (s *Session) ClearBlockingAgentHandoffs() {
	if s == nil {
		return
	}
	s.mu.Lock()
	var items []protocol.Item
	for id, task := range s.agentTasks {
		if task.Handoff == nil || !task.Handoff.Blocking() {
			continue
		}
		task.Handoff = nil
		s.agentTasks[id] = task
		items = append(items, s.appendItemLocked(protocol.Item{
			Kind: protocol.ItemAgentHandoff,
			AgentHandoff: &protocol.AgentHandoffItem{
				AgentID:  id,
				Blocking: false,
			},
		}))
	}
	sink := s.durableSink
	s.mu.Unlock()
	_ = s.persistDurableItems(items, sink)
}

func (s *Session) UpsertAgentTask(state AgentTaskState) {
	if s == nil {
		return
	}
	state.ID = strings.TrimSpace(state.ID)
	if state.ID == "" {
		return
	}

	s.mu.Lock()
	if s.agentTasks == nil {
		s.agentTasks = make(map[string]AgentTaskState)
	}
	existing, exists := s.agentTasks[state.ID]
	if !exists {
		existing = AgentTaskState{ID: state.ID, Status: AgentStatusPending, CreatedAt: time.Now()}
		s.agentTaskOrder = append(s.agentTaskOrder, state.ID)
	}
	merged := mergeAgentTaskState(existing, state)
	s.agentTasks[state.ID] = merged
	var item protocol.Item
	var sink DurableSink
	if state.Handoff != nil {
		item = s.appendItemLocked(protocol.Item{
			Kind:         protocol.ItemAgentHandoff,
			AgentHandoff: agentHandoffToProtocol(state.ID, *state.Handoff),
		})
		sink = s.durableSink
	}
	s.mu.Unlock()
	if item.Kind != "" {
		_ = s.persistDurableItem(item, sink)
	}
}

func (s *Session) RecordAgentTaskProgress(id, toolName, summary string, at time.Time) {
	id = strings.TrimSpace(id)
	toolName = strings.TrimSpace(toolName)
	if s == nil || id == "" || toolName == "" {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agentTasks == nil {
		s.agentTasks = make(map[string]AgentTaskState)
	}
	task, exists := s.agentTasks[id]
	if !exists {
		task = AgentTaskState{ID: id, Status: AgentStatusRunning, CreatedAt: at, StartedAt: at}
		s.agentTaskOrder = append(s.agentTaskOrder, id)
	}
	task.LastToolName = toolName
	task.LastActivityAt = at
	task.RecentActivity = append(task.RecentActivity, AgentTaskActivity{
		ToolName: toolName,
		Summary:  strings.TrimSpace(summary),
		At:       at,
	})
	if len(task.RecentActivity) > 12 {
		task.RecentActivity = append([]AgentTaskActivity(nil), task.RecentActivity[len(task.RecentActivity)-12:]...)
	}
	s.agentTasks[id] = task
}

func mergeAgentTaskState(existing, next AgentTaskState) AgentTaskState {
	merged := existing
	merged.ID = strings.TrimSpace(next.ID)
	if role := strings.TrimSpace(next.Role); role != "" {
		merged.Role = role
	}
	if description := strings.TrimSpace(next.Description); description != "" {
		merged.Description = description
	}
	if prompt := strings.TrimSpace(next.Prompt); prompt != "" {
		merged.Prompt = prompt
	}
	if next.Status != "" {
		merged.Status = next.Status
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
	if next.Handoff != nil {
		merged.Handoff = cloneAgentHandoff(next.Handoff)
	}
	if next.ParentTurn != 0 {
		merged.ParentTurn = next.ParentTurn
	}
	if toolName := strings.TrimSpace(next.LastToolName); toolName != "" {
		merged.LastToolName = toolName
	}
	if len(next.RecentActivity) > 0 {
		merged.RecentActivity = cloneAgentTaskActivity(next.RecentActivity)
	}
	return merged
}

func (s *Session) SetRuntimeNote(text string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	text = strings.TrimSpace(text)
	s.hookOutputSet = true
	if text == "" {
		s.hookOutput.Note = nil
		s.syncLegacyHookStateLocked()
		return
	}
	s.hookOutput.Note = &hooks.NoteResult{
		Message:    text,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}
	s.syncLegacyHookStateLocked()
}

func (s *Session) SetMemorySummary(text string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memorySummary = strings.TrimSpace(text)
}

func (s *Session) SetHookOverlays(overlays []hooks.Overlay) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hookOutputSet = true
	s.hookOutput.Overlays = append([]hooks.OverlayResult(nil), overlays...)
	s.syncLegacyHookStateLocked()
}

func (s *Session) SetHookOverlay(overlay hooks.Overlay) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.TrimSpace(overlay.Key)
	if key == "" {
		return
	}
	s.hookOutputSet = true
	for i := range s.hookOutput.Overlays {
		if strings.EqualFold(strings.TrimSpace(s.hookOutput.Overlays[i].Key), key) {
			s.hookOutput.Overlays[i] = overlay
			s.syncLegacyHookStateLocked()
			return
		}
	}
	s.hookOutput.Overlays = append(s.hookOutput.Overlays, overlay)
	s.syncLegacyHookStateLocked()
}

func (s *Session) ClearHookOverlay(key string) {
	if s == nil {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hookOutputSet = true
	filtered := s.hookOutput.Overlays[:0]
	for _, overlay := range s.hookOutput.Overlays {
		if strings.EqualFold(strings.TrimSpace(overlay.Key), key) {
			continue
		}
		filtered = append(filtered, overlay)
	}
	s.hookOutput.Overlays = append([]hooks.OverlayResult(nil), filtered...)
	s.syncLegacyHookStateLocked()
}

func (s *Session) SetHookOutput(output hooks.ExecutionOutput) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hookOutputSet = true
	s.hookOutput = cloneHookOutput(output)
	s.syncLegacyHookStateLocked()
}

func (s *Session) SetMode(mode Mode) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mode = normalizeMode(mode)
	s.mode = mode
}

func normalizeMode(mode Mode) Mode {
	switch mode {
	case ModeInspect, ModePlan, ModeImplement, ModeValidate, ModeReview, ModePreview:
		return mode
	default:
		return ModeChat
	}
}

func (s *Session) SetTaskState(state TaskState) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	objective := strings.TrimSpace(state.Objective)
	requiredVerification := strings.TrimSpace(state.RequiredVerification)
	if objective == "" && requiredVerification == "" {
		s.taskState = nil
		s.mode = ModeChat
		return
	}
	s.taskState = &TaskState{
		Objective:            objective,
		RequiredVerification: requiredVerification,
		Operation:            strings.TrimSpace(state.Operation),
		SourceRef:            strings.TrimSpace(state.SourceRef),
		TargetBranch:         strings.TrimSpace(state.TargetBranch),
	}
	s.mode = modeFromOperation(state.Operation)
}

func modeFromOperation(operation string) Mode {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "overview", "inspect":
		return ModeInspect
	case "plan":
		return ModePlan
	case "implement":
		return ModeImplement
	case "validate":
		return ModeValidate
	case "review":
		return ModeReview
	case "preview":
		return ModePreview
	default:
		return ModeChat
	}
}

func cloneTaskState(state *TaskState) *TaskState {
	if state == nil {
		return nil
	}
	cloned := *state
	return &cloned
}

func (s *Session) SetPlanState(state PlanState) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(state.Steps) == 0 {
		s.planState = nil
		return
	}
	cloned := PlanState{
		Explanation: strings.TrimSpace(state.Explanation),
		Steps:       make([]PlanStep, 0, len(state.Steps)),
	}
	for _, step := range state.Steps {
		cloned.Steps = append(cloned.Steps, PlanStep{
			Step:    strings.TrimSpace(step.Step),
			Status:  strings.TrimSpace(step.Status),
			Blocker: strings.TrimSpace(step.Blocker),
		})
	}
	s.planState = &cloned
}

func clonePlanState(state *PlanState) *PlanState {
	if state == nil {
		return nil
	}
	cloned := PlanState{
		Explanation: state.Explanation,
		Steps:       append([]PlanStep(nil), state.Steps...),
	}
	return &cloned
}

func cloneAgentTasksLocked(order []string, tasks map[string]AgentTaskState) []AgentTaskState {
	if len(order) == 0 || len(tasks) == 0 {
		return nil
	}
	out := make([]AgentTaskState, 0, len(order))
	for _, id := range order {
		task, ok := tasks[id]
		if !ok {
			continue
		}
		task.RecentActivity = cloneAgentTaskActivity(task.RecentActivity)
		task.Handoff = cloneAgentHandoff(task.Handoff)
		out = append(out, task)
	}
	return out
}

func cloneAgentTaskActivity(in []AgentTaskActivity) []AgentTaskActivity {
	if len(in) == 0 {
		return nil
	}
	return append([]AgentTaskActivity(nil), in...)
}

func cloneAgentHandoff(in *AgentHandoff) *AgentHandoff {
	if in == nil {
		return nil
	}
	out := &AgentHandoff{
		RemainingActions: append([]AgentFollowupAction(nil), in.RemainingActions...),
		Incidents:        make([]AgentWorkspaceIncident, 0, len(in.Incidents)),
	}
	for _, incident := range in.Incidents {
		incident.Paths = append([]string(nil), incident.Paths...)
		out.Incidents = append(out.Incidents, incident)
	}
	if len(out.Incidents) == 0 {
		out.Incidents = nil
	}
	return out
}

func agentHandoffToProtocol(agentID string, handoff AgentHandoff) *protocol.AgentHandoffItem {
	out := &protocol.AgentHandoffItem{
		AgentID:  strings.TrimSpace(agentID),
		Blocking: handoff.Blocking(),
	}
	for _, action := range handoff.RemainingActions {
		out.RemainingActions = append(out.RemainingActions, protocol.AgentFollowupActionItem{
			Kind:             string(action.Kind),
			TargetPath:       strings.TrimSpace(action.TargetPath),
			Description:      strings.TrimSpace(action.Description),
			SuggestedCommand: strings.TrimSpace(action.SuggestedCommand),
			Blocking:         action.Blocking,
		})
	}
	for _, incident := range handoff.Incidents {
		out.Incidents = append(out.Incidents, protocol.AgentWorkspaceIncidentItem{
			Kind:        string(incident.Kind),
			Paths:       append([]string(nil), incident.Paths...),
			Description: strings.TrimSpace(incident.Description),
			Blocking:    incident.Blocking,
		})
	}
	return out
}

func cloneHookOutput(output hooks.ExecutionOutput) hooks.ExecutionOutput {
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

func (s *Session) syncLegacyHookStateLocked() {
	s.hookOverlays = append([]hooks.Overlay(nil), s.hookOutput.Overlays...)
	if s.hookOutput.Note == nil {
		s.runtimeNote = ""
		return
	}
	s.runtimeNote = strings.TrimSpace(s.hookOutput.Note.Message)
}

func (s *Session) QueuePendingInput(text string) {
	if s == nil || strings.TrimSpace(text) == "" {
		return
	}
	s.mu.Lock()
	trimmed := strings.TrimSpace(text)
	s.pendingInput = append(s.pendingInput, trimmed)
	item := s.appendItemLocked(protocol.Item{
		Kind:        protocol.ItemTurnContext,
		TurnContext: &protocol.TurnContextItem{Input: trimmed, Mode: "queued_input"},
	})
	sink := s.durableSink
	s.mu.Unlock()
	_ = s.persistDurableItem(item, sink)
}

func (s *Session) HasPendingInput() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pendingInput) > 0
}

func (s *Session) TakePendingInput() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pendingInput) == 0 {
		return nil
	}
	out := append([]string(nil), s.pendingInput...)
	s.pendingInput = nil
	return out
}

func (s *Session) MarkInterrupted() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.interrupted = true
	if s.turn == 0 || s.turnHasTerminalLocked(fmt.Sprintf("turn-%d", s.turn)) {
		s.mu.Unlock()
		return
	}
	item := s.appendItemLocked(protocol.Item{
		Kind:         protocol.ItemTurnComplete,
		TurnID:       fmt.Sprintf("turn-%d", s.turn),
		TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusInterrupted},
	})
	sink := s.durableSink
	s.mu.Unlock()
	_ = s.persistDurableItem(item, sink)
}

func (s *Session) turnHasTerminalLocked(turnID string) bool {
	for _, item := range s.items {
		if item.TurnID == turnID && item.IsTerminal() {
			return true
		}
	}
	return false
}

func FormatPlanState(state PlanState) string {
	var parts []string
	if explanation := strings.TrimSpace(state.Explanation); explanation != "" {
		parts = append(parts, "Explanation: "+explanation)
	}
	parts = append(parts, "Plan:")
	for _, step := range state.Steps {
		line := fmt.Sprintf("- [%s] %s", strings.TrimSpace(step.Status), strings.TrimSpace(step.Step))
		if blocker := strings.TrimSpace(step.Blocker); blocker != "" {
			line += " (blocker: " + blocker + ")"
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, "\n")
}

func (s *Session) compact(keep int) bool {
	if s == nil {
		return false
	}
	if keep < 1 {
		keep = 1
	}
	s.mu.Lock()

	// Phase 1: drop whole turns when the session has more than `keep` of them.
	dropCount := len(s.recentInputs) - keep
	if dropCount < 0 {
		dropCount = 0
	}
	droppedTurnIDs := map[string]bool{}
	var droppedTurns []TurnRecord
	if dropCount > 0 {
		droppedTurns = append([]TurnRecord(nil), s.turns[:min(dropCount, len(s.turns))]...)
		for _, turn := range droppedTurns {
			droppedTurnIDs[fmt.Sprintf("turn-%d", turn.Number)] = true
		}
	}

	live := s.liveItemsLocked()
	var shadowed []string
	for _, item := range live {
		if droppedTurnIDs[item.TurnID] && contributesToPrompt(item.Kind) {
			shadowed = append(shadowed, item.Ref)
		}
	}

	// Phase 2: a long autonomous run is a single turn, so there are no turns to
	// drop and phase 1 frees nothing. Shadow the oldest balanced span of steps
	// instead, which is the only thing that bounds such a session.
	if len(shadowed) == 0 {
		shadowed = s.shadowSpanLocked(live)
	}
	if len(shadowed) == 0 {
		// Report honestly: claiming success here reset the failure circuit
		// breaker and hid a session that could no longer be compacted.
		s.mu.Unlock()
		return false
	}

	if dropCount > 0 {
		droppedRecentInputs := append([]string(nil), s.recentInputs[:dropCount]...)
		s.recentInputs = append([]string(nil), s.recentInputs[dropCount:]...)
		if len(s.turns) > dropCount {
			s.turns = append([]TurnRecord(nil), s.turns[dropCount:]...)
		} else {
			s.turns = nil
		}
		s.compactedTurns += len(droppedRecentInputs)
		if parts := summarizeCompactedTurns(droppedTurns); len(parts) > 0 {
			s.compactionSummary = strings.TrimSpace(s.compactionSummary + " " + strings.Join(parts, " | "))
		}
	}

	item := s.appendCompactionLocked(shadowed, nil)
	sink := s.durableSink
	s.mu.Unlock()
	_ = s.persistDurableItem(item, sink)
	return true
}

func summarizeCompactedTurns(turns []TurnRecord) []string {
	parts := make([]string, 0, len(turns))
	for _, turn := range turns {
		if summary := summarizeTurn(turn); summary != "" {
			parts = append(parts, summary)
		}
	}
	return parts
}

func summarizeTurn(turn TurnRecord) string {
	fields := make([]string, 0, 4)
	if input := strings.TrimSpace(turn.Input); input != "" {
		fields = append(fields, "user: "+input)
	}
	if len(turn.ToolCalls) > 0 {
		names := make([]string, 0, len(turn.ToolCalls))
		for _, call := range turn.ToolCalls {
			if name := strings.TrimSpace(call.Name); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			fields = append(fields, "tools: "+strings.Join(names, ", "))
		}
	}
	if outcome := strings.TrimSpace(turn.FinalResponse); outcome != "" {
		fields = append(fields, "outcome: "+outcome)
	}
	if errText := strings.TrimSpace(turn.Error); errText != "" {
		fields = append(fields, "error: "+errText)
	}
	if len(fields) == 0 {
		return ""
	}
	return fmt.Sprintf("turn %d [%s]", turn.Number, strings.Join(fields, " ; "))
}
