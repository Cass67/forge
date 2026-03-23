package tui

import (
	"strings"
	"testing"
	"time"

	"forge/internal/codexusage"
	"forge/internal/copilot"
	"forge/internal/llm"
	"forge/internal/modelcatalog"
)

func TestBuildStatsSectionsIncludesAllSections(t *testing.T) {
	sections := buildStatsSections(chatStatsData{
		chatStatusData: chatStatusData{
			Model:        "copilot/gpt-5",
			WorkDir:      "/tmp/work",
			Status:       "ready",
			Duration:     2 * time.Second,
			LastUsage:    llm.Usage{InputTokens: 80, OutputTokens: 20},
			SessionUsage: llm.Usage{InputTokens: 120, OutputTokens: 30},
			RequestMode:  "responses",
			ContextUsed:  1200,
			ContextLimit: 8000,
			ModelInfo:    &modelcatalog.ModelInfo{Reasoning: true},
		},
	})

	if len(sections) != 5 {
		t.Fatalf("sections = %d, want 5", len(sections))
	}
	got := make([]string, 0, len(sections))
	for _, section := range sections {
		got = append(got, section.Title)
	}
	want := []string{"Turn", "Session", "Provider", "Model", "Diagnostics"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("section titles = %#v, want %#v", got, want)
	}
}

func TestBuildStatsOverlayRendersCopilotQuota(t *testing.T) {
	rendered := renderStatsOverlayPanel(chatTheme{}, chatStatsData{
		chatStatusData: chatStatusData{
			Model:        "copilot/gpt-5",
			Duration:     time.Second,
			LastUsage:    llm.Usage{InputTokens: 80, OutputTokens: 20},
			SessionUsage: llm.Usage{InputTokens: 120, OutputTokens: 30},
			RequestMode:  "responses",
			CopilotLive: &copilot.UserQuota{
				Windows: map[string]llm.CopilotQuota{
					"premium": {
						Type:      "premium_interactions",
						Remaining: 143,
					},
				},
			},
			ModelInfo: &modelcatalog.ModelInfo{Reasoning: true},
		},
	}, 100, 24)

	if !strings.Contains(rendered, "Turn") || !strings.Contains(rendered, "Provider") {
		t.Fatalf("rendered overlay missing section titles: %s", rendered)
	}
	if !strings.Contains(rendered, "Copilot") || !strings.Contains(rendered, "143") {
		t.Fatalf("rendered overlay missing live Copilot quota: %s", rendered)
	}
}

func TestBuildStatsOverlayRendersCodexUsage(t *testing.T) {
	rendered := renderStatsOverlayPanel(chatTheme{}, chatStatsData{
		chatStatusData: chatStatusData{
			Model:        "openai/gpt-5",
			Duration:     time.Second,
			SessionUsage: llm.Usage{InputTokens: 120, OutputTokens: 30},
			RequestMode:  "responses",
			CodexUsage: &codexusage.Snapshot{
				Plan: "pro",
				Primary: &codexusage.Window{
					UsedPercent: 20,
					ResetIn:     "5h",
				},
			},
		},
	}, 100, 24)

	if !strings.Contains(rendered, "OpenAI/Codex") || !strings.Contains(rendered, "pro") {
		t.Fatalf("rendered overlay missing Codex usage summary: %s", rendered)
	}
	if !strings.Contains(rendered, "5h") {
		t.Fatalf("rendered overlay missing codex reset window: %s", rendered)
	}
}

func TestBuildStatsOverlayFallsBackWhenDataMissing(t *testing.T) {
	rendered := renderStatsOverlayPanel(chatTheme{}, chatStatsData{}, 100, 24)

	for _, want := range []string{"Turn", "Session", "Provider", "Model", "Diagnostics", "unavailable"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered overlay missing %q: %s", want, rendered)
		}
	}
}

func TestBuildStatsOverlayShowsRequestModeAndMetadata(t *testing.T) {
	rendered := renderStatsOverlayPanel(chatTheme{}, chatStatsData{
		chatStatusData: chatStatusData{
			Model:        "openai/gpt-5",
			RequestMode:  "responses",
			ModelInfo:    &modelcatalog.ModelInfo{Reasoning: true, Temperature: true, ToolCall: true},
			ContextUsed:  1200,
			ContextLimit: 8000,
		},
	}, 100, 24)

	for _, want := range []string{"responses", "reasoning", "temperature", "tools"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered overlay missing %q: %s", want, rendered)
		}
	}
}

func TestBuildStatsOverlayShowsModelLimits(t *testing.T) {
	rendered := renderStatsOverlayPanel(chatTheme{}, chatStatsData{
		chatStatusData: chatStatusData{
			Model: "openai/gpt-5",
			ModelInfo: &modelcatalog.ModelInfo{
				ContextWindow: 128000,
				OutputLimit:   8192,
				Reasoning:     true,
			},
		},
	}, 100, 24)

	for _, want := range []string{"128000", "8192", "reasoning"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered overlay missing %q: %s", want, rendered)
		}
	}
}

func TestBuildStatusLine2ShowsContextForOpenRouterModels(t *testing.T) {
	line := buildStatusLine2(chatStatusData{
		Model:        "openrouter/arcee-ai/trinity-large-preview:free",
		Status:       "ready",
		SessionUsage: llm.Usage{InputTokens: 120, OutputTokens: 30},
		ModelInfo: &modelcatalog.ModelInfo{
			ContextWindow: 131072,
			Temperature:   true,
			ToolCall:      true,
		},
	})

	for _, want := range []string{"ready", "session 150 tok", "ctx 150/131072"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status line missing %q: %s", want, line)
		}
	}
}
