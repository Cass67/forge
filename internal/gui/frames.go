package gui

import (
	"encoding/json"

	"forge/internal/llm"
	"forge/internal/protocol"
	"forge/internal/tui"
)

// Wire protocol between the embedded web app and the Go server.
//
// Server -> client: init, event, approval, threads, history, action_result,
// done. Client -> server: input, approve, action (see clientFrame).
//
// Events are passed through as wireEvent, a JSON-safe projection of
// llm.Event (error values are rendered to strings, durations to ms).

type providerFrame struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Status       string `json:"status,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
}

type skillFrame struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type initFrame struct {
	Type        string          `json:"type"`
	Model       string          `json:"model"`
	WorkDir     string          `json:"work_dir"`
	Models      []string        `json:"models"`
	Providers   []providerFrame `json:"providers"`
	Effort      string          `json:"effort,omitempty"`
	Efforts     []string        `json:"efforts"`
	Skills      []skillFrame    `json:"skills"`
	ThreadID    string          `json:"thread_id,omitempty"`
	RequestMode string          `json:"request_mode,omitempty"`
}

type wireEvent struct {
	Kind             string     `json:"kind"`
	Agent            string     `json:"agent,omitempty"`
	Text             string     `json:"text,omitempty"`
	PassName         string     `json:"pass_name,omitempty"`
	Pass             int        `json:"pass,omitempty"`
	Round            int        `json:"round,omitempty"`
	IsError          bool       `json:"is_error,omitempty"`
	Content          string     `json:"content,omitempty"`
	DurationMS       int64      `json:"duration_ms,omitempty"`
	Usage            *llm.Usage `json:"usage,omitempty"`
	ContextUsed      int        `json:"context_used,omitempty"`
	ContextLimit     int        `json:"context_limit,omitempty"`
	ContextEstimated bool       `json:"context_estimated,omitempty"`
	SubAgent         string     `json:"sub_agent,omitempty"`
	Error            string     `json:"error,omitempty"`
}

type eventFrame struct {
	Type  string    `json:"type"`
	Event wireEvent `json:"event"`
}

type wireAction struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
	Path    string `json:"path,omitempty"`
}

type approvalFrame struct {
	Type   string     `json:"type"`
	Action wireAction `json:"action"`
}

type threadsFrame struct {
	Type  string              `json:"type"`
	Items []tui.ThreadSummary `json:"items"`
}

type historyFrame struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Items    []protocol.Item `json:"items"`
	Restored int             `json:"restored,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type actionResultFrame struct {
	Type    string          `json:"type"`
	Name    string          `json:"name"`
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type doneFrame struct {
	Type string `json:"type"`
}

// toWireEvent projects an llm.Event into its JSON-safe wire form.
func toWireEvent(ev llm.Event) wireEvent {
	w := wireEvent{
		Kind:             string(ev.Kind),
		Agent:            ev.Agent,
		Text:             ev.Text,
		PassName:         ev.PassName,
		Pass:             ev.Pass,
		Round:            ev.Round,
		IsError:          ev.IsError,
		Content:          ev.Content,
		DurationMS:       ev.Duration.Milliseconds(),
		ContextUsed:      ev.ContextUsed,
		ContextLimit:     ev.ContextLimit,
		ContextEstimated: ev.ContextEstimated,
		SubAgent:         ev.SubAgent,
	}
	if ev.Usage.InputTokens != 0 || ev.Usage.OutputTokens != 0 {
		w.Usage = &ev.Usage
	}
	if ev.Err != nil {
		w.Error = ev.Err.Error()
	}
	return w
}
