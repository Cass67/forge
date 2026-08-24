package gui

import (
	"forge/internal/auth"
	"forge/internal/llm"
	"forge/internal/providerauth"
	"forge/internal/skills"
	"forge/internal/tui"
)

// InitPayload is the chrome the window renders before any message is sent.
// Ready is false while the chat runtime is still starting, in which case the
// frontend waits for the forge:ready event and asks again.
type InitPayload struct {
	Ready       bool              `json:"ready"`
	Model       string            `json:"model"`
	WorkDir     string            `json:"work_dir"`
	Models      []string          `json:"models"`
	Providers   []ProviderPayload `json:"providers"`
	Effort      string            `json:"effort,omitempty"`
	Efforts     []string          `json:"efforts"`
	Skills      []SkillPayload    `json:"skills"`
	ThreadID    string            `json:"thread_id,omitempty"`
	RequestMode string            `json:"request_mode,omitempty"`
	Yolo        bool              `json:"yolo"`
	// Notice explains a startup decision, such as a saved model dropped
	// because nothing serves it any more.
	Notice string `json:"notice,omitempty"`
}

type ProviderPayload struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Status       string `json:"status,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
	// SignedIn reports whether credentials are currently stored.
	SignedIn bool `json:"signed_in"`
	// Interactive is true for providers that sign in through a browser flow
	// rather than by pasting an API key.
	Interactive bool `json:"interactive"`
}

type SkillPayload struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// wireAction is a pending tool approval request.
type wireAction struct {
	Tool      string `json:"tool"`
	Summary   string `json:"summary"`
	Detail    string `json:"detail,omitempty"`
	Path      string `json:"path,omitempty"`
	Workspace string `json:"workspace,omitempty"`
}

// DonePayload marks the end of a turn for one workspace's runtime.
type DonePayload struct {
	Workspace string `json:"workspace"`
}

// wireEvent is a JSON-safe projection of llm.Event: errors render to strings
// and durations to milliseconds, so the hot event type needs no JSON tags.
type wireEvent struct {
	Kind             string     `json:"kind"`
	Workspace        string     `json:"workspace,omitempty"`
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

func providerPayloads(in []tui.ProviderOption) []ProviderPayload {
	tokens, _ := auth.Load()
	out := make([]ProviderPayload, 0, len(in))
	for _, p := range in {
		out = append(out, ProviderPayload{
			ID:           p.ID,
			Label:        p.Label,
			Status:       p.Status,
			DefaultModel: p.DefaultModel,
			SignedIn:     tokens != nil && providerauth.HasCredential(tokens, p.ID),
			Interactive:  providerauth.Interactive(p.ID),
		})
	}
	return out
}

func skillPayloads(in []skills.Skill) []SkillPayload {
	out := make([]SkillPayload, 0, len(in))
	for _, sk := range in {
		out = append(out, SkillPayload{Name: sk.Name, Description: sk.Description})
	}
	return out
}
