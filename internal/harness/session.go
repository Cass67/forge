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
	resolvedClass := class
	resolvedClass.TaskText = strings.TrimSpace(class.TaskText)
	step := Step{Lane: inferredLane(resolvedClass), Kind: StepLocal}
	if step.Lane == LaneStrictAction {
		step.Kind = StepStrictLocal
	}
	obs = normalizeObservation(step, resolvedClass, s.state, obs)

	s.state.LastFamily = resolvedClass.Family
	s.state.LastTopicKey = strings.TrimSpace(resolvedClass.TopicKey)
	s.state.LastResponse = strings.TrimSpace(obs.Response)
	s.state.LastMeta = deriveMetaIntent(resolvedClass)
	if s.state.LastMeta != MetaNone {
		s.state.LastMetaTurn = s.state.Turn
	} else {
		s.state.LastMetaTurn = 0
	}
	s.state.PendingAction = PendingAction{}

	if obs.Status != ObservationComplete {
		return
	}

	applyThreadLedger(&s.state, resolvedClass, obs)

	if pending := finalizePendingAction(obs.PendingAction, s.state.Turn); !pending.IsZero() {
		s.state.PendingAction = pending
	}
	if artifact := finalizeArtifactSnapshot(obs.Runtime.Artifact, s.state.Turn); !artifact.IsZero() {
		s.state.LastArtifact = artifact
	}
	if preview := finalizePreviewSnapshot(obs.Runtime.Preview, s.state.Turn); !preview.IsZero() {
		s.state.LastPreview = preview
	}

	topic := strings.TrimSpace(obs.TopicKey)
	if topic == "" {
		topic = strings.TrimSpace(resolvedClass.TopicKey)
	}
	if topic == "" {
		return
	}
	if !retainsEvidence(resolvedClass) {
		return
	}

	s.state.LastEvidence = EvidenceSnapshot{
		Turn:     s.state.Turn,
		TopicKey: topic,
		Summary:  firstNonEmpty(strings.TrimSpace(obs.Summary), strings.TrimSpace(obs.Response)),
	}
}

func finalizePendingAction(action PendingAction, turn int) PendingAction {
	if action.IsZero() {
		return PendingAction{}
	}
	if action.SetAtTurn <= 0 {
		action.SetAtTurn = turn
	}
	action.TaskText = strings.TrimSpace(action.TaskText)
	action.TopicKey = strings.TrimSpace(action.TopicKey)
	action.ResponsePostlude = strings.TrimSpace(action.ResponsePostlude)
	if !action.CanStayLocal && action.Family != FamilyResearch {
		action.CanStayLocal = true
	}
	return action
}

func finalizeArtifactSnapshot(artifact ArtifactSnapshot, turn int) ArtifactSnapshot {
	if artifact.IsZero() {
		return ArtifactSnapshot{}
	}
	if artifact.Turn <= 0 {
		artifact.Turn = turn
	}
	artifact.Handle = strings.TrimSpace(artifact.Handle)
	artifact.Path = strings.TrimSpace(artifact.Path)
	artifact.MIMEType = strings.TrimSpace(artifact.MIMEType)
	return artifact
}

func finalizePreviewSnapshot(preview PreviewSnapshot, turn int) PreviewSnapshot {
	if preview.IsZero() {
		return PreviewSnapshot{}
	}
	if preview.Turn <= 0 {
		preview.Turn = turn
	}
	preview.Status = strings.TrimSpace(preview.Status)
	preview.Handle = strings.TrimSpace(preview.Handle)
	preview.Root = strings.TrimSpace(preview.Root)
	preview.Path = strings.TrimSpace(preview.Path)
	preview.URL = strings.TrimSpace(preview.URL)
	return preview
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
