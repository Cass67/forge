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
	RoleTool      Role = "tool"
)

// ToolParam describes one parameter of a tool for native tool calling.
type ToolParam struct {
	Name        string
	Type        string // "string", "integer", "boolean"
	Description string
	Required    bool
}

// ToolSchema describes structured tool input for native tool calling.
type ToolSchema struct {
	Type                 string
	Description          string
	Properties           map[string]*ToolSchema
	Items                *ToolSchema
	Required             []string
	Enum                 []string
	AdditionalProperties *bool
}

// ToolDef describes a tool for native structured tool calling via the provider API.
type ToolDef struct {
	Name        string
	Description string
	Parameters  []ToolParam
	Schema      *ToolSchema
}

// NativeToolCall is a completed tool call returned via the provider's native tool-calling API.
type NativeToolCall struct {
	ID       string
	Name     string
	ArgsJSON string
}

// Message is a single turn sent to an LLM.
type Message struct {
	Role             Role
	Content          string
	ContentParts     []MessageContentPart // non-nil for multimodal messages (text + images)
	ReasoningContent string               // reasoning/thinking tokens that must be replayed (e.g. DeepSeek thinking mode)
	ToolCalls        []NativeToolCall     // non-nil when Role==RoleAssistant and model made native tool calls
	ToolCallID       string               // non-empty when Role==RoleTool (result message)
}

// MessageContentPart represents one part of a multimodal message content.
type MessageContentPart struct {
	Type  string // "text" or "image"
	Text  string
	Image *ImageContent
}

// ImageContent describes an image to be sent to a vision-capable model.
type ImageContent struct {
	Path     string
	MIMEType string
	Width    int
	Height   int
}

// HasContentParts reports whether the message carries multimodal content parts.
func (m Message) HasContentParts() bool {
	return len(m.ContentParts) > 0
}

// Token is a single streamed chunk from an LLM response.
type Token struct {
	Text             string
	Done             bool
	Err              error
	ToolCall         *NativeToolCall // non-nil when provider returns a native tool call via StreamWithTools
	ReasoningContent string          // reasoning/thinking token text (DeepSeek, etc.)
}

// HasReasoning reports whether the message carries reasoning content that must
// be replayed to the provider on subsequent turns.
func (m Message) HasReasoning() bool {
	return m.Role == RoleAssistant && m.ReasoningContent != ""
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

// NativeToolCaller is optionally implemented by drivers that support provider-native
// structured tool calling. When implemented, the react runner uses this path instead
// of any text-based tool-call shim.
type NativeToolCaller interface {
	StreamWithTools(ctx context.Context, messages []Message, tools []ToolDef, out chan<- Token) error
}

// NativeToolOptions carries per-request controls for provider-native tool calling.
type NativeToolOptions struct {
	RequireToolCall bool
}

// NativeToolCallerWithOptions is an optional extension for drivers that support
// per-request native tool-calling controls such as forcing a tool call before prose.
type NativeToolCallerWithOptions interface {
	StreamWithToolsOptions(ctx context.Context, messages []Message, tools []ToolDef, opts NativeToolOptions, out chan<- Token) error
}

type EventKind string

const (
	EventToken           EventKind = "token"
	EventRetry           EventKind = "retry"
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
	EventProgress        EventKind = "progress"
	EventAgentTask       EventKind = "agent_task"
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
	Content  string        // diff content or full tool output
	Duration time.Duration // turn duration (EventStats)
	Usage    Usage         // token usage (EventStats)
	SubAgent string        // non-empty when event comes from a delegated sub-agent
}
