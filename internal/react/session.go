package react

import (
	"fmt"
	"strings"
	"sync"

	"forge/internal/llm"
)

type TurnToolCall struct {
	Name string
}

type TaskState struct {
	Objective            string
	RequiredVerification string
	Operation            string
	SourceRef            string
	TargetBranch         string
}

type PlanStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type PlanState struct {
	Explanation string     `json:"explanation,omitempty"`
	Steps       []PlanStep `json:"steps"`
}

type TurnRecord struct {
	Number        int
	Input         string
	FinalResponse string
	ToolCalls     []TurnToolCall
	Error         string
}

type SessionSnapshot struct {
	Turn              int
	LastInput         string
	RecentInputs      []string
	History           []llm.Message
	Turns             []TurnRecord
	CompactedTurns    int
	CompactionSummary string
	RuntimeNote       string
	TaskState         *TaskState
	PlanState         *PlanState
	PendingInput      []string
	Interrupted       bool
}

type Session struct {
	mu                sync.Mutex
	turn              int
	lastInput         string
	recentInputs      []string
	history           []llm.Message
	turns             []TurnRecord
	compactedTurns    int
	compactionSummary string
	runtimeNote       string
	taskState         *TaskState
	planState         *PlanState
	pendingInput      []string
	interrupted       bool
}

func NewSession() *Session {
	return &Session{}
}

func (s *Session) RecordInput(input string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turn++
	s.lastInput = input
	s.recentInputs = append(s.recentInputs, input)
	s.history = append(s.history, llm.Message{Role: llm.RoleUser, Content: input})
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
	if s == nil || len(calls) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.turns) > 0 {
		last := &s.turns[len(s.turns)-1]
		last.ToolCalls = make([]TurnToolCall, 0, len(calls))
		for _, call := range calls {
			last.ToolCalls = append(last.ToolCalls, TurnToolCall{Name: strings.TrimSpace(call.Name)})
		}
	}
	s.history = append(s.history, llm.Message{
		Role:      llm.RoleAssistant,
		ToolCalls: append([]llm.NativeToolCall(nil), calls...),
	})
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
		Turn:              s.turn,
		LastInput:         s.lastInput,
		RecentInputs:      append([]string(nil), s.recentInputs...),
		History:           append([]llm.Message(nil), s.history...),
		Turns:             append([]TurnRecord(nil), s.turns...),
		CompactedTurns:    s.compactedTurns,
		CompactionSummary: s.compactionSummary,
		RuntimeNote:       s.runtimeNote,
		TaskState:         cloneTaskState(s.taskState),
		PlanState:         clonePlanState(s.planState),
		PendingInput:      append([]string(nil), s.pendingInput...),
		Interrupted:       s.interrupted,
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
	s.recentInputs = nil
	s.history = nil
	s.turns = nil
	s.compactedTurns = 0
	s.compactionSummary = ""
	s.runtimeNote = ""
	s.taskState = nil
	s.planState = nil
	s.pendingInput = nil
	s.interrupted = false
}

func (s *Session) SetRuntimeNote(text string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeNote = strings.TrimSpace(text)
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
		return
	}
	s.taskState = &TaskState{
		Objective:            objective,
		RequiredVerification: requiredVerification,
		Operation:            strings.TrimSpace(state.Operation),
		SourceRef:            strings.TrimSpace(state.SourceRef),
		TargetBranch:         strings.TrimSpace(state.TargetBranch),
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
			Step:   strings.TrimSpace(step.Step),
			Status: strings.TrimSpace(step.Status),
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
		parts = append(parts, fmt.Sprintf("- [%s] %s", strings.TrimSpace(step.Status), strings.TrimSpace(step.Step)))
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
	if len(s.recentInputs) <= keep {
		return false
	}
	dropCount := len(s.recentInputs) - keep
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
