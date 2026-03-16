package llm

import "context"

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

type EventKind string

const (
	EventToken    EventKind = "token"
	EventRoundEnd EventKind = "round_end"
	EventPassEnd  EventKind = "pass_end"
	EventError    EventKind = "error"
	EventDone     EventKind = "done"
	EventAbort    EventKind = "abort"
)

// Event carries session progress from the runner to the TUI.
type Event struct {
	Kind  EventKind
	Agent string // "writer", "auditor", "summarizer"
	Text  string
	Pass  int
	Round int
	Err   error
}
