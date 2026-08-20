package agent

import (
	"errors"
	"fmt"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/llm"
	"forge/internal/secscan"
)

// EventRenderer sends render calls as llm.Event values to a channel.
// Used by the live chat TUI instead of printing to stdout.
type EventRenderer struct {
	events             chan<- llm.Event
	approvalCh         chan tools.Action
	responseCh         chan bool
	label              string // displayed in TUI header (e.g. "dispatch", "agent")
	lastToolProgressAt time.Time
}

func NewEventRenderer(events chan<- llm.Event) *EventRenderer {
	return &EventRenderer{
		events:     events,
		approvalCh: make(chan tools.Action, 1),
		responseCh: make(chan bool, 1),
		label:      "forge",
	}
}

// SetLabel changes the agent label shown in the TUI chat header.
func (r *EventRenderer) SetLabel(label string) {
	r.label = label
}

func (r *EventRenderer) AgentToken(text string) {
	r.events <- llm.Event{Kind: llm.EventToken, Agent: r.label, Text: text}
}

func (r *EventRenderer) AgentText(text string) {
	r.events <- llm.Event{Kind: llm.EventToken, Agent: r.label, Text: text}
}

func (r *EventRenderer) AgentReasoning(text string) {
	r.events <- llm.Event{Kind: llm.EventReasoning, Agent: r.label, Text: text}
}

func (r *EventRenderer) Retry(msg string) {
	r.events <- llm.Event{Kind: llm.EventRetry, Agent: r.label, Text: msg}
}

func (r *EventRenderer) ToolCall(name, summary string) {
	r.events <- llm.Event{Kind: llm.EventToolCall, Agent: name, Text: summary}
	if progress := progressLine(r.label, name, summary); progress != "" {
		now := time.Now()
		if r.shouldThrottleToolProgress(now) {
			return
		}
		r.events <- llm.Event{Kind: llm.EventProgress, Agent: r.label, Text: progress}
		r.lastToolProgressAt = now
	}
}

func (r *EventRenderer) shouldThrottleToolProgress(now time.Time) bool {
	if r.lastToolProgressAt.IsZero() {
		return false
	}
	// Keep progress chatty but avoid trace-like bursts of per-tool lines.
	return now.Sub(r.lastToolProgressAt) < 750*time.Millisecond
}

func (r *EventRenderer) ToolResult(name, output, diff string, isError bool) {
	r.events <- llm.Event{
		Kind:    llm.EventToolResult,
		Agent:   name,
		Text:    output,
		Content: diff,
		IsError: isError,
	}
}

func (r *EventRenderer) Stats(duration time.Duration, usage llm.Usage) {
	r.events <- llm.Event{
		Kind:     llm.EventStats,
		Duration: duration,
		Usage:    usage,
	}
}

func (r *EventRenderer) StatsWithContext(duration time.Duration, usage llm.Usage, contextUsed, contextLimit int) {
	r.events <- llm.Event{
		Kind:             llm.EventStats,
		Duration:         duration,
		Usage:            usage,
		ContextUsed:      contextUsed,
		ContextLimit:     contextLimit,
		ContextEstimated: true,
	}
}

func (r *EventRenderer) Error(msg string) {
	r.events <- llm.Event{Kind: llm.EventError, Text: msg}
}

func (r *EventRenderer) Info(msg string) {
	r.events <- llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: msg}
}

func (r *EventRenderer) Progress(msg string) {
	r.events <- llm.Event{Kind: llm.EventProgress, Agent: r.label, Text: msg}
}

func (r *EventRenderer) AgentTaskState(payload string) {
	r.events <- llm.Event{Kind: llm.EventAgentTask, Content: redactEventPayload(payload)}
}

func redactEventPayload(payload string) string {
	if payload == "" {
		return ""
	}
	scanner := secscan.NewDefaultScanner()
	return secscan.Redact(payload, scanner.Scan(payload))
}

// TurnDone signals the agent finished processing a user message.
func (r *EventRenderer) TurnDone() {
	r.events <- llm.Event{Kind: llm.EventDone}
}

// ApprovalChan returns the channel the live view reads approval requests from.
func (r *EventRenderer) ApprovalChan() <-chan tools.Action {
	return r.approvalCh
}

// ResponseChan returns the channel the live view writes approval responses to.
func (r *EventRenderer) ResponseChan() chan<- bool {
	return r.responseCh
}

// LiveApproval returns an ApprovalFunc that routes approval through the live TUI.
// Includes a 5-minute timeout to prevent permanent deadlocks if the TUI stops responding.
func (r *EventRenderer) LiveApproval() tools.ApprovalFunc {
	return func(action tools.Action) (bool, error) {
		r.events <- llm.Event{
			Kind:  llm.EventToolCall,
			Agent: action.Tool,
			Text:  fmt.Sprintf("[approval needed] %s", action.Summary),
		}
		r.approvalCh <- action

		select {
		case approved := <-r.responseCh:
			if approved {
				r.events <- llm.Event{Kind: llm.EventToolResult, Agent: action.Tool, Text: "approved"}
			} else {
				r.events <- llm.Event{Kind: llm.EventToolResult, Agent: action.Tool, Text: "denied"}
			}
			return approved, nil
		case <-time.After(5 * time.Minute):
			r.events <- llm.Event{Kind: llm.EventToolResult, Agent: action.Tool, Text: "denied (timeout)"}
			return false, errors.New("approval timed out after 5 minutes")
		}
	}
}

// SubAgentRenderer wraps an EventRenderer and tags all events with a sub-agent
// label. Sub-agent tokens are routed to the tools pane instead of the chat.
type SubAgentRenderer struct {
	parent *EventRenderer
	role   string
}

func NewSubAgentRenderer(parent *EventRenderer, role string) *SubAgentRenderer {
	return &SubAgentRenderer{parent: parent, role: role}
}

func (r *SubAgentRenderer) AgentToken(text string) {
	r.parent.events <- llm.Event{Kind: llm.EventToken, Agent: "agent", Text: text, SubAgent: r.role}
}

func (r *SubAgentRenderer) AgentText(text string) {
	r.parent.events <- llm.Event{Kind: llm.EventToken, Agent: "agent", Text: text, SubAgent: r.role}
}

func (r *SubAgentRenderer) ToolCall(name, summary string) {
	r.parent.events <- llm.Event{Kind: llm.EventToolCall, Agent: name, Text: summary, SubAgent: r.role}
	if progress := progressLine(r.role, name, summary); progress != "" {
		now := time.Now()
		if r.parent.shouldThrottleToolProgress(now) {
			return
		}
		r.parent.events <- llm.Event{Kind: llm.EventProgress, Agent: r.role, Text: progress, SubAgent: r.role}
		r.parent.lastToolProgressAt = now
	}
}

func (r *SubAgentRenderer) ToolResult(name, output, diff string, isError bool) {
	r.parent.events <- llm.Event{
		Kind:     llm.EventToolResult,
		Agent:    name,
		Text:     output,
		Content:  diff,
		IsError:  isError,
		SubAgent: r.role,
	}
}

func (r *SubAgentRenderer) Stats(duration time.Duration, usage llm.Usage) {
	r.parent.events <- llm.Event{
		Kind:     llm.EventStats,
		Duration: duration,
		Usage:    usage,
		SubAgent: r.role,
	}
}

func (r *SubAgentRenderer) StatsWithContext(duration time.Duration, usage llm.Usage, contextUsed, contextLimit int) {
	r.parent.events <- llm.Event{
		Kind:             llm.EventStats,
		Duration:         duration,
		Usage:            usage,
		ContextUsed:      contextUsed,
		ContextLimit:     contextLimit,
		ContextEstimated: true,
		SubAgent:         r.role,
	}
}

func (r *SubAgentRenderer) Error(msg string) {
	r.parent.events <- llm.Event{Kind: llm.EventError, Text: msg, SubAgent: r.role}
}

func (r *SubAgentRenderer) Info(msg string) {
	r.parent.events <- llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: msg, SubAgent: r.role}
}

func (r *SubAgentRenderer) Progress(msg string) {
	r.parent.events <- llm.Event{Kind: llm.EventProgress, Agent: r.role, Text: msg, SubAgent: r.role}
}
