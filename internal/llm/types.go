package llm

import (
	"context"
	"fmt"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single turn sent to an LLM.
type Message struct {
	Role    Role
	Content string
}

// Token is a single streamed chunk from an LLM response.
type Token struct {
	Text string
	Done bool
	Err  error
}

// Driver is the interface every LLM provider must implement.
type Driver interface {
	Name() string
	Stream(ctx context.Context, messages []Message, out chan<- Token) error
}

// UsageReporter is optionally implemented by drivers that can report token usage.
type UsageReporter interface {
	LastUsage() Usage
}

// Params holds per-model generation parameters.
type Params struct {
	MaxTokens   int
	Temperature float64 // -1 means unset (use provider default)
}

// Configurable is optionally implemented by drivers that accept generation params.
type Configurable interface {
	SetParams(p Params)
}

// ConversationResetter is optionally implemented by stateful drivers that can
// discard provider-side conversation state when local history is cleared.
type ConversationResetter interface {
	ResetConversation()
}

// RequestModeReporter is optionally implemented by drivers that can describe
// the context/state path used for the most recent request.
type RequestModeReporter interface {
	LastRequestMode() string
}

type EventKind string

const (
	EventToken           EventKind = "token"
	EventRoundStart      EventKind = "round_start"
	EventRoundEnd        EventKind = "round_end"
	EventPassStart       EventKind = "pass_start"
	EventPassEnd         EventKind = "pass_end"
	EventAgentDone       EventKind = "agent_done"
	EventWarning         EventKind = "warning"
	EventError           EventKind = "error"
	EventDone            EventKind = "done"
	EventAbort           EventKind = "abort"
	EventFeedbackRequest EventKind = "feedback_request"
	EventToolCall        EventKind = "tool_call"
	EventToolResult      EventKind = "tool_result"
	EventStats           EventKind = "stats"
)

// PassName returns a human-readable label for a 1-based pass number.
func PassName(pass int) string {
	switch pass {
	case 1:
		return "correctness"
	case 2:
		return "refactor"
	case 3:
		return "security"
	case 4:
		return "prod-ready"
	default:
		return fmt.Sprintf("pass %d", pass)
	}
}

// Event carries session progress from the runner to the TUI.
type Event struct {
	Kind     EventKind
	Agent    string // "writer", "auditor", "summarizer", or tool name
	Text     string
	PassName string // custom pass name (if set, overrides PassName() lookup)
	Pass     int
	Round    int
	Err      error
	// Chat frontend fields
	IsError  bool          // true if tool result is an error
	Content  string        // diff content or full tool output (for /expand)
	Duration time.Duration // turn duration (EventStats)
	Usage    Usage         // token usage (EventStats)
}
