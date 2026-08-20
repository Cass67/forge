package gui

import (
	"encoding/json"
	"errors"
	"sort"

	"forge/internal/protocol"
)

var (
	errNoRestore     = errors.New("restore is not available in this session")
	errUnknownAction = errors.New("unknown action")
)

// clientFrame is one client -> server message.
type clientFrame struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	OK      bool            `json:"ok"`
	Name    string          `json:"name,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// readItems fetches stored items for a thread, newest thread's full history.
func readItems(fn func(string) []protocol.Item, threadID string) []protocol.Item {
	if fn == nil || threadID == "" {
		return nil
	}
	items := fn(threadID)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Seq < items[j].Seq })
	return items
}
