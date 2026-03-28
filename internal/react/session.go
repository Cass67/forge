package react

import (
	"strings"
	"sync"
)

type SessionSnapshot struct {
	Turn              int
	LastInput         string
	RecentInputs      []string
	CompactedTurns    int
	CompactionSummary string
}

type Session struct {
	mu                sync.Mutex
	turn              int
	lastInput         string
	recentInputs      []string
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
	return s.turn
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
		CompactedTurns:    s.compactedTurns,
		CompactionSummary: s.compactionSummary,
	}
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
