package react

import (
	"strings"
	"sync"

	"forge/internal/agent"
	"forge/internal/llm"
)

type TurnToolCall struct {
	Name string
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

func (s *Session) CompleteTurn(turn int, response string, toolCalls []agent.ToolCall, err error) {
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
			s.turns[i].ToolCalls = make([]TurnToolCall, 0, len(toolCalls))
			for _, call := range toolCalls {
				s.turns[i].ToolCalls = append(s.turns[i].ToolCalls, TurnToolCall{Name: strings.TrimSpace(call.Name)})
			}
		}
		if err != nil {
			s.turns[i].Error = strings.TrimSpace(err.Error())
		}
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

func (s *Session) AppendToolResults(results string) {
	if s == nil || strings.TrimSpace(results) == "" {
		return
	}
	s.AppendUserMessage(results)
}

func (s *Session) Messages(systemPrompt string) []llm.Message {
	if s == nil {
		if strings.TrimSpace(systemPrompt) == "" {
			return nil
		}
		return []llm.Message{{Role: llm.RoleSystem, Content: strings.TrimSpace(systemPrompt)}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := make([]llm.Message, 0, len(s.history)+1)
	if strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: strings.TrimSpace(systemPrompt)})
	}
	messages = append(messages, s.history...)
	return messages
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
	dropped := append([]string(nil), s.recentInputs[:dropCount]...)
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
	s.compactedTurns += len(dropped)
	parts := make([]string, 0, len(dropped))
	for _, item := range dropped {
		item = strings.TrimSpace(item)
		if item != "" {
			parts = append(parts, item)
		}
	}
	if len(parts) > 0 {
		s.compactionSummary = strings.TrimSpace(s.compactionSummary + " " + strings.Join(parts, " | "))
	}
	return true
}
