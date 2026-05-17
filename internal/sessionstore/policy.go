package sessionstore

import (
	"strings"

	"forge/internal/protocol"
	"forge/internal/secscan"
)

type PersistencePolicy struct {
	MaxToolResultBytes int
}

func DefaultPersistencePolicy() PersistencePolicy {
	return PersistencePolicy{MaxToolResultBytes: 10 * 1024}
}

func (p PersistencePolicy) Apply(item protocol.Item) protocol.Item {
	scanner := secscan.NewDefaultScanner()
	redact := func(text string) string {
		return secscan.Redact(text, scanner.Scan(text))
	}
	if item.Message != nil {
		item.Message.Text = redact(item.Message.Text)
	}
	if item.TurnContext != nil {
		item.TurnContext.Input = redact(item.TurnContext.Input)
	}
	if item.Compaction != nil {
		item.Compaction.Summary = redact(item.Compaction.Summary)
	}
	if item.Failure != nil {
		item.Failure.Decision.Feedback = redact(item.Failure.Decision.Feedback)
	}
	if item.ToolResult != nil {
		rawBytes := len(item.ToolResult.Text)
		redacted := redact(item.ToolResult.Text)
		if p.MaxToolResultBytes > 0 && len(redacted) > p.MaxToolResultBytes {
			item.ToolResult.OriginalBytes = rawBytes
			item.ToolResult.Text = redacted[:p.MaxToolResultBytes]
			item.ToolResult.Truncated = true
		} else {
			item.ToolResult.Text = redacted
		}
	}
	if item.ToolResult != nil {
		item.ToolResult.Diff = redact(item.ToolResult.Diff)
	}
	if item.ToolCall != nil && len(item.ToolCall.Args) > 0 {
		redacted := map[string]any{}
		for k, v := range item.ToolCall.Args {
			if strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "secret") || strings.Contains(strings.ToLower(k), "key") {
				redacted[k] = "<REDACTED>"
				continue
			}
			redacted[k] = redactArgValue(v, scanner)
		}
		item.ToolCall.Args = redacted
	}
	return item
}

func redactArgValue(v any, scanner *secscan.Scanner) any {
	switch val := v.(type) {
	case string:
		return secscan.Redact(val, scanner.Scan(val))
	case map[string]any:
		out := map[string]any{}
		for k, nested := range val {
			if strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "secret") || strings.Contains(strings.ToLower(k), "key") {
				out[k] = "<REDACTED>"
				continue
			}
			out[k] = redactArgValue(nested, scanner)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, nested := range val {
			out[i] = redactArgValue(nested, scanner)
		}
		return out
	default:
		return v
	}
}
