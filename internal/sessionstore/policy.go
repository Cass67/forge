package sessionstore

import (
	"strings"

	"forge/internal/protocol"
)

type PersistencePolicy struct {
	MaxToolResultBytes int
}

func DefaultPersistencePolicy() PersistencePolicy {
	return PersistencePolicy{MaxToolResultBytes: 10 * 1024}
}

func (p PersistencePolicy) Apply(item protocol.Item) protocol.Item {
	if item.ToolResult != nil && p.MaxToolResultBytes > 0 && len(item.ToolResult.Text) > p.MaxToolResultBytes {
		item.ToolResult.OriginalBytes = len(item.ToolResult.Text)
		item.ToolResult.Text = item.ToolResult.Text[:p.MaxToolResultBytes]
		item.ToolResult.Truncated = true
	}
	if item.ToolCall != nil && len(item.ToolCall.Args) > 0 {
		redacted := map[string]any{}
		for k, v := range item.ToolCall.Args {
			if strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "secret") || strings.Contains(strings.ToLower(k), "key") {
				redacted[k] = "<REDACTED>"
				continue
			}
			redacted[k] = v
		}
		item.ToolCall.Args = redacted
	}
	return item
}
