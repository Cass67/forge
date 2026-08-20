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
		item.Message.ReasoningContent = redact(item.Message.ReasoningContent)
		for i := range item.Message.ContentParts {
			item.Message.ContentParts[i].Text = redact(item.Message.ContentParts[i].Text)
		}
	}
	if item.TurnContext != nil {
		item.TurnContext.Input = redact(item.TurnContext.Input)
	}
	if item.Compaction != nil {
		item.Compaction.Summary = redact(item.Compaction.Summary)
		for i := range item.Compaction.Replacements {
			item.Compaction.Replacements[i].Text = redact(item.Compaction.Replacements[i].Text)
		}
	}
	if item.Failure != nil {
		item.Failure.Decision.Feedback = redact(item.Failure.Decision.Feedback)
	}
	if item.ToolResult != nil {
		rawBytes := len(item.ToolResult.Text)
		redacted := redact(item.ToolResult.Text)
		if p.MaxToolResultBytes > 0 && len(redacted) > p.MaxToolResultBytes {
			if item.ToolResult.OriginalBytes == 0 {
				item.ToolResult.OriginalBytes = rawBytes
			}
			item.ToolResult.Text = redacted[:p.MaxToolResultBytes]
			item.ToolResult.Truncated = true
		} else {
			item.ToolResult.Text = redacted
		}
		item.ToolResult.Diff = redact(item.ToolResult.Diff)
	}
	if item.ToolCall != nil && len(item.ToolCall.Args) > 0 {
		redacted := map[string]any{}
		sensitive := false
		for k, v := range item.ToolCall.Args {
			if isSensitiveArgKey(k) {
				redacted[k] = "<REDACTED>"
				sensitive = true
				continue
			}
			redacted[k] = redactArgValue(v, scanner)
		}
		item.ToolCall.Args = redacted
		// ArgsJSON is byte-exact and cannot express a key-name redaction while
		// preserving key order. When a sensitive key is present the exact bytes
		// must not reach disk at all, so drop them and let replay fall back to
		// the redacted map.
		if sensitive {
			item.ToolCall.ArgsJSON = ""
		}
	}
	if item.ToolCall != nil && item.ToolCall.ArgsJSON != "" {
		item.ToolCall.ArgsJSON = redact(item.ToolCall.ArgsJSON)
	}
	return item
}

func isSensitiveArgKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "key")
}

func redactArgValue(v any, scanner *secscan.Scanner) any {
	switch val := v.(type) {
	case string:
		return secscan.Redact(val, scanner.Scan(val))
	case map[string]any:
		out := map[string]any{}
		for k, nested := range val {
			if isSensitiveArgKey(k) {
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
