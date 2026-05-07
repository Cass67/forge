package react

import "fmt"

type CompactionMode string

const (
	CompactionNone        CompactionMode = "none"
	CompactionMicro       CompactionMode = "micro"
	CompactionSummarize   CompactionMode = "summarize"
	CompactionReactive    CompactionMode = "reactive"
	CompactionUserPartial CompactionMode = "user_partial"
)

type CompactionDecision struct {
	Mode       CompactionMode
	Reason     string
	KeepTurns  int
	DropTurns  int
	SummaryLen int
}

type CompactionHookPayload struct {
	Mode          CompactionMode `json:"mode"`
	Reason        string         `json:"reason"`
	KeepTurns     int            `json:"keep_turns"`
	DroppedTurns  int            `json:"dropped_turns"`
	SummaryLength int            `json:"summary_length"`
	Changed       bool           `json:"changed"`
	CircuitOpen   bool           `json:"circuit_open"`
}

type CompactionConfig struct {
	KeepTurns            int
	HistoryPressureTurns int
	LargeToolResultBytes int
	MaxFailures          int
}

type CompactionManager struct {
	cfg      CompactionConfig
	failures int
}

func NewCompactionManager(cfg CompactionConfig) *CompactionManager {
	if cfg.KeepTurns < 1 {
		cfg.KeepTurns = 40
	}
	if cfg.HistoryPressureTurns < 1 {
		cfg.HistoryPressureTurns = cfg.KeepTurns
	}
	if cfg.LargeToolResultBytes < 1 {
		cfg.LargeToolResultBytes = 64 * 1024
	}
	return &CompactionManager{cfg: cfg}
}

func (m *CompactionManager) Decide(snapshot SessionSnapshot) CompactionDecision {
	if m == nil || m.CircuitOpen() {
		return CompactionDecision{Mode: CompactionNone, Reason: "compaction circuit open"}
	}
	for _, msg := range snapshot.History {
		if len(msg.Content) > m.cfg.LargeToolResultBytes {
			return CompactionDecision{Mode: CompactionMicro, Reason: "large tool result", KeepTurns: m.cfg.KeepTurns}
		}
	}
	if len(snapshot.RecentInputs) > m.cfg.HistoryPressureTurns || snapshot.Turn > m.cfg.HistoryPressureTurns {
		return CompactionDecision{Mode: CompactionSummarize, Reason: "history pressure", KeepTurns: m.cfg.KeepTurns}
	}
	return CompactionDecision{Mode: CompactionNone, Reason: "below threshold", KeepTurns: m.cfg.KeepTurns}
}

func (m *CompactionManager) Reactive(keep int, reason string) CompactionDecision {
	if keep < 1 {
		keep = m.cfg.KeepTurns
	}
	return CompactionDecision{Mode: CompactionReactive, Reason: reason, KeepTurns: keep}
}

func (m *CompactionManager) UserPartial(keep int) CompactionDecision {
	if keep < 1 {
		keep = m.cfg.KeepTurns
	}
	return CompactionDecision{Mode: CompactionUserPartial, Reason: fmt.Sprintf("preserve recent %d turns", keep), KeepTurns: keep}
}

func (m *CompactionManager) Apply(session *Session, decision CompactionDecision) bool {
	if m == nil || session == nil {
		return false
	}
	switch decision.Mode {
	case CompactionSummarize, CompactionReactive, CompactionUserPartial:
		keep := decision.KeepTurns
		if keep < 1 {
			keep = m.cfg.KeepTurns
		}
		changed := CompactSessionHistory(session, keep)
		if changed {
			m.failures = 0
		} else {
			m.RecordFailure()
		}
		return changed
	case CompactionMicro:
		return false
	default:
		return false
	}
}

func (m *CompactionManager) RecordFailure() {
	if m != nil {
		m.failures++
	}
}

func (m *CompactionManager) CircuitOpen() bool {
	return m != nil && m.cfg.MaxFailures > 0 && m.failures >= m.cfg.MaxFailures
}
