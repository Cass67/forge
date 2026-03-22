package llm

import "sync"

// Usage holds token counts for a single LLM call.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// UsageEntry records token usage for one agent call within a session.
type UsageEntry struct {
	Agent string `json:"agent"`
	Model string `json:"model"`
	Pass  int    `json:"pass"`
	Round int    `json:"round"`
	Usage Usage  `json:"usage"`
}

// UsageTracker accumulates token usage across an entire session.
type UsageTracker struct {
	mu      sync.Mutex
	entries []UsageEntry
}

func NewUsageTracker() *UsageTracker {
	return &UsageTracker{}
}

func (t *UsageTracker) Record(entry UsageEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = append(t.entries, entry)
}

func (t *UsageTracker) Entries() []UsageEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]UsageEntry(nil), t.entries...)
}

func (t *UsageTracker) Total() Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	var total Usage
	for _, e := range t.entries {
		total.InputTokens += e.Usage.InputTokens
		total.OutputTokens += e.Usage.OutputTokens
	}
	return total
}

// EstimateTokens provides a rough token count from text length (~4 chars per token).
func EstimateTokens(text string) int {
	return (len(text) + 3) / 4
}
