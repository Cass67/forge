package agent

import (
	"fmt"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/llm"
)

// EventRenderer sends render calls as llm.Event values to a channel.
// Used by the live chat TUI instead of printing to stdout.
type EventRenderer struct {
	events     chan<- llm.Event
	approvalCh chan tools.Action
	responseCh chan bool
}

func NewEventRenderer(events chan<- llm.Event) *EventRenderer {
	return &EventRenderer{
		events:     events,
		approvalCh: make(chan tools.Action, 1),
		responseCh: make(chan bool, 1),
	}
}

func (r *EventRenderer) AgentToken(text string) {
	r.events <- llm.Event{Kind: llm.EventToken, Agent: "agent", Text: text}
}

func (r *EventRenderer) AgentText(text string) {
	r.events <- llm.Event{Kind: llm.EventToken, Agent: "agent", Text: text}
}

func (r *EventRenderer) ToolCall(name, summary string) {
	r.events <- llm.Event{Kind: llm.EventToolCall, Agent: name, Text: summary}
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

func (r *EventRenderer) Error(msg string) {
	r.events <- llm.Event{Kind: llm.EventError, Text: msg}
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
func (r *EventRenderer) LiveApproval() tools.ApprovalFunc {
	return func(action tools.Action) (bool, error) {
		r.events <- llm.Event{
			Kind:  llm.EventToolCall,
			Agent: action.Tool,
			Text:  fmt.Sprintf("[approval needed] %s", action.Summary),
		}
		r.approvalCh <- action
		approved := <-r.responseCh
		if approved {
			r.events <- llm.Event{Kind: llm.EventToolResult, Agent: action.Tool, Text: "approved"}
		} else {
			r.events <- llm.Event{Kind: llm.EventToolResult, Agent: action.Tool, Text: "denied"}
		}
		return approved, nil
	}
}
