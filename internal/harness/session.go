package harness

import (
	"strings"
	"sync"
	"time"
)

type Session struct {
	mu    sync.RWMutex
	state SessionState
}

func NewSession() *Session {
	return &Session{}
}

func (s *Session) BeginTurn(text string) UserTurn {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Turn++
	return UserTurn{
		Text:       strings.TrimSpace(text),
		Turn:       s.state.Turn,
		ReceivedAt: time.Now(),
	}
}

func (s *Session) Snapshot() SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Session) Apply(class Classification, obs Observation) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state.LastFamily = class.Family
	s.state.LastTopicKey = strings.TrimSpace(class.TopicKey)
	s.state.LastResponse = strings.TrimSpace(obs.Response)
	s.state.LastMeta = deriveMetaIntent(class)
	if s.state.LastMeta != MetaNone {
		s.state.LastMetaTurn = s.state.Turn
	} else {
		s.state.LastMetaTurn = 0
	}

	if obs.Status != ObservationComplete {
		return
	}

	topic := strings.TrimSpace(obs.TopicKey)
	if topic == "" {
		topic = strings.TrimSpace(class.TopicKey)
	}
	if topic == "" {
		return
	}
	if !retainsEvidence(class) {
		return
	}

	s.state.LastEvidence = EvidenceSnapshot{
		Turn:     s.state.Turn,
		TopicKey: topic,
		Summary:  firstNonEmpty(strings.TrimSpace(obs.Summary), strings.TrimSpace(obs.Response)),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func retainsEvidence(class Classification) bool {
	switch class.Family {
	case FamilyImplement, FamilyDebug:
		return false
	default:
		return true
	}
}

func deriveMetaIntent(class Classification) MetaIntent {
	if class.NeedsPolicyGuard {
		return MetaPromptBoundary
	}
	if class.Family == FamilyAnswer && class.NeedsTerseAnswer {
		return MetaProcess
	}
	return MetaNone
}
