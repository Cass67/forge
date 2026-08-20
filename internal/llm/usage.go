package llm

// CopilotQuota holds GitHub Copilot premium usage snapshot data when available.
type CopilotQuota struct {
	Type             string  `json:"type,omitempty"`
	Included         int     `json:"included,omitempty"`
	Used             int     `json:"used,omitempty"`
	Remaining        int     `json:"remaining,omitempty"`
	PercentRemaining float64 `json:"percent_remaining,omitempty"`
	Unlimited        bool    `json:"unlimited,omitempty"`
	ResetAt          string  `json:"reset_at,omitempty"`
}

// Usage holds token counts for a single LLM call.
//
// InputTokens is always the total prompt billed, cache hits included. Providers
// disagree here — OpenAI's prompt_tokens already includes cached tokens while
// Anthropic's input_tokens excludes them — so the Anthropic driver adds the
// cache counts back to keep the field comparable across providers.
// CachedInputTokens is the subset of InputTokens served from cache.
type Usage struct {
	InputTokens       int           `json:"input_tokens"`
	OutputTokens      int           `json:"output_tokens"`
	CachedInputTokens int           `json:"cached_input_tokens,omitempty"`
	CacheWriteTokens  int           `json:"cache_write_tokens,omitempty"`
	CopilotQuota      *CopilotQuota `json:"copilot_quota,omitempty"`
}

// EstimateTokens provides a rough token count from text length (~4 chars per token).
func EstimateTokens(text string) int {
	return (len(text) + 3) / 4
}
