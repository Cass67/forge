package react

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"forge/internal/hooks"
	"forge/internal/llm"
	"forge/internal/secscan"
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
	ParentTurn     int                 `json:"parent_turn,omitempty"`
	LastToolName   string              `json:"last_tool_name,omitempty"`
	RecentActivity []AgentTaskActivity `json:"recent_activity,omitempty"`
}

type DelegationActionKind string

const (
	DelegationActionNone            DelegationActionKind = "none"
	DelegationActionWriteDoc        DelegationActionKind = "write_doc"
	DelegationActionRunVerification DelegationActionKind = "run_verification"
	DelegationActionCommit          DelegationActionKind = "commit"
	DelegationActionAskUser         DelegationActionKind = "ask_user"
)

type DelegationActionState struct {
	Kind        DelegationActionKind `json:"kind"`
	TargetPath  string               `json:"target_path,omitempty"`
	SourceAgent string               `json:"source_agent,omitempty"`
	Description string               `json:"description,omitempty"`
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
		case "in_progress", "blocked":
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

type SessionSnapshot struct {
	Turn                    int
	LastInput               string
	InitialInput            string
	RecentInputs            []string
	History                 []llm.Message
	Turns                   []TurnRecord
	CompactedTurns          int
	CompactionSummary       string
	MemorySummary           string
	HookOutputSet           bool
	HookOutput              hooks.ExecutionOutput
	HookOverlays            []hooks.Overlay
	RuntimeNote             string
	Mode                    Mode
	TaskState               *TaskState
	PlanState               *PlanState
	AgentTasks              []AgentTaskState
	PendingDelegationAction *DelegationActionState
	PendingInput            []string
	Interrupted             bool
}

type Session struct {
	mu                      sync.Mutex
	turn                    int
	lastInput               string
	initialInput            string
	recentInputs            []string
	history                 []llm.Message
	turns                   []TurnRecord
	compactedTurns          int
	compactionSummary       string
	memorySummary           string
	hookOutputSet           bool
	hookOutput              hooks.ExecutionOutput
	hookOverlays            []hooks.Overlay
	runtimeNote             string
	mode                    Mode
	taskState               *TaskState
	planState               *PlanState
	agentTasks              map[string]AgentTaskState
	agentTaskOrder          []string
	pendingDelegationAction *DelegationActionState
	pendingInput            []string
	interrupted             bool
}

func NewSession() *Session {
	return &Session{mode: ModeChat}
}

func (s *Session) RecordInput(input string) int {
	return s.RecordInputWithParts(input, nil)
}

func (s *Session) RecordInputWithParts(input string, parts []llm.MessageContentPart) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turn++
	s.lastInput = input
	if strings.TrimSpace(s.initialInput) == "" {
		s.initialInput = strings.TrimSpace(input)
	}
	s.recentInputs = append(s.recentInputs, input)
	msg := llm.Message{Role: llm.RoleUser, Content: input}
	if len(parts) > 0 {
		msg.ContentParts = parts
	}
	s.history = append(s.history, msg)
	s.turns = append(s.turns, TurnRecord{
		Number: s.turn,
		Input:  input,
	})
	return s.turn
}

func (s *Session) CompleteTurn(turn int, response string, toolCalls []TurnToolCall, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
		if err != nil {
			s.turns[i].Error = strings.TrimSpace(err.Error())
		}
		s.interrupted = false
		return
	}
}

func (s *Session) AppendAssistantMessage(text string) {
	if s == nil || strings.TrimSpace(text) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, llm.Message{Role: llm.RoleAssistant, Content: strings.TrimSpace(text)})
}

func (s *Session) AppendUserMessage(text string) {
	if s == nil || strings.TrimSpace(text) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, llm.Message{Role: llm.RoleUser, Content: strings.TrimSpace(text)})
}

// AppendAssistantWithToolCalls records an assistant message that contains native
// tool calls (may have empty text content). Used by the native tool calling path.
func (s *Session) AppendAssistantWithToolCalls(calls []llm.NativeToolCall) {
	s.AppendAssistantToolTurn("", calls)
}

// AppendAssistantToolTurn records an assistant message that may include both a
// short natural-language preamble and native tool calls.
func (s *Session) AppendAssistantToolTurn(text string, calls []llm.NativeToolCall) {
	if s == nil || len(calls) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	text = redactRuntimeText(strings.TrimSpace(text))
	if len(s.turns) > 0 {
		last := &s.turns[len(s.turns)-1]
		last.ToolCalls = make([]TurnToolCall, 0, len(calls))
		for _, call := range calls {
			last.ToolCalls = append(last.ToolCalls, TurnToolCall{Name: strings.TrimSpace(call.Name)})
		}
	}
	s.history = append(s.history, llm.Message{
		Content:   text,
		Role:      llm.RoleAssistant,
		ToolCalls: redactNativeToolCalls(calls),
	})
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
	for i := len(s.history) - 1; i >= 0; i-- {
		if s.history[i].Role == llm.RoleAssistant {
			s.history[i].ReasoningContent = redactRuntimeText(reasoning)
			return
		}
	}
}

// AppendNativeToolResult records a tool execution result matched to a specific
// tool call ID. Used by the native tool calling path.
func (s *Session) AppendNativeToolResult(toolCallID, result string) {
	if s == nil || strings.TrimSpace(toolCallID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: toolCallID,
		Content:    result,
	})
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
	return SessionSnapshot{
		Turn:                    s.turn,
		LastInput:               s.lastInput,
		InitialInput:            s.initialInput,
		RecentInputs:            append([]string(nil), s.recentInputs...),
		History:                 append([]llm.Message(nil), s.history...),
		Turns:                   append([]TurnRecord(nil), s.turns...),
		CompactedTurns:          s.compactedTurns,
		CompactionSummary:       s.compactionSummary,
		MemorySummary:           s.memorySummary,
		HookOutputSet:           s.hookOutputSet,
		HookOutput:              cloneHookOutput(s.hookOutput),
		HookOverlays:            append([]hooks.Overlay(nil), s.hookOverlays...),
		RuntimeNote:             s.runtimeNote,
		Mode:                    s.mode,
		TaskState:               cloneTaskState(s.taskState),
		PlanState:               clonePlanState(s.planState),
		AgentTasks:              cloneAgentTasksLocked(s.agentTaskOrder, s.agentTasks),
		PendingDelegationAction: cloneDelegationActionState(s.pendingDelegationAction),
		PendingInput:            append([]string(nil), s.pendingInput...),
		Interrupted:             s.interrupted,
	}
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
	s.history = nil
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
	s.pendingDelegationAction = nil
	s.pendingInput = nil
	s.interrupted = false
}

func (s *Session) SetPendingDelegationAction(state DelegationActionState) {
	if s == nil {
		return
	}
	state.Kind = normalizeDelegationActionKind(state.Kind)
	if state.Kind == DelegationActionNone {
		s.ClearPendingDelegationAction()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingDelegationAction = &DelegationActionState{
		Kind:        state.Kind,
		TargetPath:  strings.TrimSpace(state.TargetPath),
		SourceAgent: strings.TrimSpace(state.SourceAgent),
		Description: strings.TrimSpace(state.Description),
	}
}

func (s *Session) ClearPendingDelegationAction() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingDelegationAction = nil
}

func normalizeDelegationActionKind(kind DelegationActionKind) DelegationActionKind {
	switch kind {
	case DelegationActionWriteDoc, DelegationActionRunVerification, DelegationActionCommit, DelegationActionAskUser:
		return kind
	default:
		return DelegationActionNone
	}
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
	defer s.mu.Unlock()
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

func cloneDelegationActionState(state *DelegationActionState) *DelegationActionState {
	if state == nil {
		return nil
	}
	cloned := *state
	return &cloned
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
	defer s.mu.Unlock()
	s.pendingInput = append(s.pendingInput, strings.TrimSpace(text))
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
	defer s.mu.Unlock()
	s.interrupted = true
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
	defer s.mu.Unlock()
	if len(s.recentInputs) <= keep && len(s.history) <= keep*10 {
		return false
	}
	dropCount := len(s.recentInputs) - keep
	if dropCount <= 0 {
		dropCount = 0
	}
	droppedRecentInputs := append([]string(nil), s.recentInputs[:dropCount]...)
	droppedTurns := append([]TurnRecord(nil), s.turns[:min(dropCount, len(s.turns))]...)
	s.recentInputs = append([]string(nil), s.recentInputs[dropCount:]...)
	if len(s.turns) > dropCount {
		s.turns = append([]TurnRecord(nil), s.turns[dropCount:]...)
	} else {
		s.turns = nil
	}
	if len(s.turns) == 0 {
		s.history = nil
	} else {
		firstTurn := s.turns[0].Input
		cut := 0
		for idx, msg := range s.history {
			if msg.Role == llm.RoleUser && strings.TrimSpace(msg.Content) == strings.TrimSpace(firstTurn) {
				cut = idx
				break
			}
		}
		s.history = append([]llm.Message(nil), s.history[cut:]...)
	}
	s.compactedTurns += len(droppedRecentInputs)
	parts := summarizeCompactedTurns(droppedTurns)
	if len(parts) > 0 {
		s.compactionSummary = strings.TrimSpace(s.compactionSummary + " " + strings.Join(parts, " | "))
	}
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
