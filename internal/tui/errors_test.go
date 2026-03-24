package tui

import (
	"testing"

	"forge/internal/llm"
)

func TestEventErrorMessageDistillsRateLimitPayload(t *testing.T) {
	ev := llm.Event{
		Kind: llm.EventError,
		Text: `all 3 attempts failed: openrouter stream (api: chat.completions, model: arcee-ai/trinity-large-preview:free): POST "https://openrouter.ai/api/v1/chat/completions": 429 Too Many Requests {"message":"Rate limit exceeded: free-models-per-min.","code":429}`,
	}

	if got := eventErrorMessage(ev); got != "429 Too Many Requests" {
		t.Fatalf("eventErrorMessage() = %q", got)
	}
}

func TestEventErrorMessageDistillsRateLimitWithoutStatusCode(t *testing.T) {
	ev := llm.Event{
		Kind: llm.EventError,
		Text: `provider error: {"message":"Rate limit exceeded: free-models-per-min."}`,
	}

	if got := eventErrorMessage(ev); got != "Rate limit exceeded" {
		t.Fatalf("eventErrorMessage() = %q", got)
	}
}
