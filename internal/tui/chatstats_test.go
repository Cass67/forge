package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

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

func TestRenderStatusHeaderUsesAppBackground(t *testing.T) {
	withTrueColorProfile(t)

	theme := chatTheme{
		AppBG:         lipgloss.Color("#112233"),
		HeaderBG:      lipgloss.Color("#445566"),
		HeaderFG:      lipgloss.Color("#ddeeff"),
		AccentPrimary: lipgloss.Color("#88aaff"),
		Text:          lipgloss.Color("#eef2f7"),
		TextDim:       lipgloss.Color("#8b97a8"),
		Border:        lipgloss.Color("#334455"),
	}

	rendered := renderStatusHeader(theme, chatStatusData{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp/work",
	}, 80)

	if strings.Contains(rendered, ansiBackground(theme.HeaderBG)) {
		t.Fatalf("header should not use header background as the surface fill: %q", rendered)
	}
	if !strings.Contains(rendered, ansiBackground(theme.AppBG)) {
		t.Fatalf("header missing app background fill: %q", rendered)
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

	for _, want := range []string{"ready", "session 150 tok", "est ctx 150/131072"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status line missing %q: %s", want, line)
		}
	}
}

func TestRenderStatusHeaderUsesSingleCompactLine(t *testing.T) {
	rendered := renderStatusHeader(lookupThemeForTest(t, "default"), chatStatusData{
		Model:        "oca/OpenAI GPT 5.4",
		ThemeID:      "default",
		WorkDir:      "/Users/mcassidy/git/work/Cerner/managability",
		Status:       "ready",
		SessionUsage: llm.Usage{InputTokens: 23122},
		LastUsage:    llm.Usage{InputTokens: 1217, OutputTokens: 212},
	}, 200)

	lines := strings.Split(strippedLine(rendered), "\n")
	if len(lines) != 1 {
		t.Fatalf("header lines = %d, want 1: %q", len(lines), rendered)
	}
	if !strings.Contains(lines[0], "FORGE") || !strings.Contains(lines[0], "oca/OpenAI GPT 5.4") || !strings.Contains(lines[0], "/Users/mcassidy/git/work/Cerner/managability") {
		t.Fatalf("header missing compact identity line: %q", lines[0])
	}
	for _, unwanted := range []string{"theme:", "ready", "session", "last"} {
		if strings.Contains(lines[0], unwanted) {
			t.Fatalf("header should omit %q: %q", unwanted, lines[0])
		}
	}
}
