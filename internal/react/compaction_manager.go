package react

import (
	"fmt"

	"forge/internal/llm"
)

type CompactionMode string

const (
	CompactionNone        CompactionMode = "none"
	CompactionMicro       CompactionMode = "micro"
	CompactionSummarize   CompactionMode = "summarize"
	CompactionReactive    CompactionMode = "reactive"
	CompactionUserPartial CompactionMode = "user_partial"
)

type CompactionDecision struct {
	Mode            CompactionMode
	Reason          string
	KeepTurns       int
	DropTurns       int
	SummaryLen      int
	ToolResultBytes int
	ProtectTail     int
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
	KeepTurns             int
	HistoryPressureTurns  int
	LargeToolResultBytes  int
	PromptBudgetBytes     int
	PromptToolResultBytes int
	MaxFailures           int
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
	if cfg.PromptBudgetBytes < 1 {
		cfg.PromptBudgetBytes = 256 * 1024
	}
	if cfg.PromptToolResultBytes < 1 {
		cfg.PromptToolResultBytes = 4 * 1024
	}
	return &CompactionManager{cfg: cfg}
}

func (m *CompactionManager) Decide(snapshot SessionSnapshot) CompactionDecision {
	if m == nil || m.CircuitOpen() {
		return CompactionDecision{Mode: CompactionNone, Reason: "compaction circuit open"}
	}
	for _, msg := range snapshot.History {
		if msg.Role == llm.RoleTool && len(msg.Content) > m.cfg.LargeToolResultBytes {
			return CompactionDecision{Mode: CompactionMicro, Reason: "large tool result", KeepTurns: m.cfg.KeepTurns}
		}
	}
	if len(snapshot.RecentInputs) > m.cfg.HistoryPressureTurns || snapshot.Turn > m.cfg.HistoryPressureTurns {
		return CompactionDecision{Mode: CompactionSummarize, Reason: "history pressure", KeepTurns: m.cfg.KeepTurns}
	}
	return CompactionDecision{Mode: CompactionNone, Reason: "below threshold", KeepTurns: m.cfg.KeepTurns}
}

func (m *CompactionManager) DecidePromptPressure(messages []llm.Message) CompactionDecision {
	if m == nil || m.CircuitOpen() || m.cfg.PromptBudgetBytes < 1 {
		return CompactionDecision{Mode: CompactionNone, Reason: "compaction unavailable"}
	}
	if estimatePromptBytes(messages) <= m.cfg.PromptBudgetBytes {
		return CompactionDecision{Mode: CompactionNone, Reason: "below prompt budget", KeepTurns: m.cfg.KeepTurns}
	}
	// Only pick micro when a compactable (non tail-protected) result exists;
	// the apply step skips the freshest microCompactProtectedTail messages, so
	// deciding on those would fire a no-op micro compaction on every step and
	// never escalate to summarization.
	compactableEnd := len(messages) - microCompactProtectedTail
	for i, msg := range messages {
		if i >= compactableEnd {
			break
		}
		if msg.Role == llm.RoleTool && len(msg.Content) > m.cfg.PromptToolResultBytes {
			// Protect the freshest results: crushing what the model is actively
			// using forces re-reads that look like tool-call loops.
			return CompactionDecision{Mode: CompactionMicro, Reason: "prompt budget", KeepTurns: m.cfg.KeepTurns, ToolResultBytes: m.cfg.PromptToolResultBytes, ProtectTail: microCompactProtectedTail}
		}
	}
	if !promptHasOlderTurns(messages) {
		return CompactionDecision{Mode: CompactionNone, Reason: "prompt budget current turn only", KeepTurns: m.cfg.KeepTurns}
	}
	return CompactionDecision{Mode: CompactionSummarize, Reason: "prompt budget", KeepTurns: m.cfg.KeepTurns}
}

func promptHasOlderTurns(messages []llm.Message) bool {
	seenUsers := 0
	for _, msg := range messages {
		if msg.Role != llm.RoleUser {
			continue
		}
		seenUsers++
		if seenUsers > 1 {
			return true
		}
	}
	return false
}

func estimatePromptBytes(messages []llm.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Role) + len(msg.Content) + len(msg.ReasoningContent) + len(msg.ToolCallID) + 16
		for _, part := range msg.ContentParts {
			total += len(part.Type) + len(part.Text)
			if part.Image != nil {
				total += len(part.Image.Path) + len(part.Image.MIMEType) + 32
			}
		}
		for _, call := range msg.ToolCalls {
			total += len(call.ID) + len(call.Name) + len(call.ArgsJSON) + 16
		}
	}
	return total
}

func estimatePromptTokens(messages []llm.Message) int {
	bytes := estimatePromptBytes(messages)
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
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
		maxBytes := decision.ToolResultBytes
		if maxBytes < 1 {
			maxBytes = m.cfg.LargeToolResultBytes
		}
		changed := MicroCompactLargeToolResults(session, maxBytes, decision.ProtectTail)
		if changed {
			m.failures = 0
		}
		return changed
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
