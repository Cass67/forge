package tui

import (
	"strings"
	"testing"

	"forge/internal/codexusage"
	"forge/internal/copilot"
	"forge/internal/llm"
)

func TestChatStatusSummaryCopilotModelUsesCopilotQuota(t *testing.T) {
	summary := buildProviderStatusSummary(chatStatusData{
		Model: "copilot/gpt-5",
		CopilotLive: &copilot.UserQuota{
			Windows: map[string]llm.CopilotQuota{
				"premium": {
					Type:      "premium_interactions",
					Remaining: 143,
				},
			},
		},
	})

	if !strings.Contains(summary, "143") {
		t.Fatalf("summary = %q, want live Copilot quota", summary)
	}
}

func TestChatStatusSummaryOtherProviderSkipsSubscriptionText(t *testing.T) {
	summary := buildProviderStatusSummary(chatStatusData{
		Model: "anthropic/claude-sonnet-4-6",
		CopilotLive: &copilot.UserQuota{
			Windows: map[string]llm.CopilotQuota{
				"premium": {
					Type:      "premium_interactions",
					Remaining: 143,
				},
			},
		},
		CodexUsage: &codexusage.Snapshot{
			Plan: "pro",
		},
	})

	lower := strings.ToLower(summary)
	if strings.Contains(lower, "copilot") || strings.Contains(lower, "codex") {
		t.Fatalf("summary = %q, want no provider subscription text", summary)
	}
}

func TestChatStatusContextSummaryIncludesRequestMode(t *testing.T) {
	summary := buildContextSummary(chatStatusData{
		ContextUsed:  1200,
		ContextLimit: 8000,
		RequestMode:  "responses",
	})

	if !strings.Contains(summary, "1200") || !strings.Contains(summary, "8000") {
		t.Fatalf("summary = %q, want context usage", summary)
	}
	if !strings.Contains(summary, "responses") {
		t.Fatalf("summary = %q, want request mode", summary)
	}
}
