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

func TestEventErrorMessageDistillsChatGPTForbiddenPayload(t *testing.T) {
	ev := llm.Event{
		Kind: llm.EventError,
		Text: `chatgpt stream (api: chat.completions, model: gpt-5.4-mini): POST "https://chatgpt.com/backend-api/codex/chat/completions": 403 Forbidden [err=POST "https://chatgpt.com/backend-api/codex/chat/completions": 403 Forbidden]`,
	}

	if got := eventErrorMessage(ev); got != "403 Forbidden — ChatGPT auth is not authorized for gpt-5.4-mini" {
		t.Fatalf("eventErrorMessage() = %q", got)
	}
}

func TestEventErrorMessageDistillsGenericForbidden(t *testing.T) {
	ev := llm.Event{
		Kind: llm.EventError,
		Text: `provider error: POST "https://example.com/v1/chat": 403 Forbidden`,
	}

	if got := eventErrorMessage(ev); got != "403 Forbidden" {
		t.Fatalf("eventErrorMessage() = %q", got)
	}
}
