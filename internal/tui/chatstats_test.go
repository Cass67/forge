package tui

import (
	"os"
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

func TestRenderStatusHeaderUsesAppBackgroundSurface(t *testing.T) {
	theme := chatTheme{
		AppBG:         lipgloss.Color("#112233"),
		HeaderBG:      lipgloss.Color("#112233"),
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

	for _, want := range []string{"FORGE", "model", "dir", "openai/gpt-5", "/tmp/work"} {
		if !strings.Contains(strippedLine(rendered), want) {
			t.Fatalf("header missing %q: %q", want, strippedLine(rendered))
		}
	}
}

func TestRenderStatusHeaderForHeightKeepsModelLineAtLowHeights(t *testing.T) {
	rendered := renderStatusHeaderForHeight(chatTheme{}, chatStatusData{
		Model:   "copilot/gpt-5",
		WorkDir: "/tmp/work",
	}, 42, 6)
	plain := strippedLine(rendered)

	if !strings.Contains(plain, "model") || !strings.Contains(plain, "copilot/gpt-5") {
		t.Fatalf("height-aware header should keep model line, got:\n%s", plain)
	}
	if !strings.Contains(plain, "dir") || !strings.Contains(plain, "work") {
		t.Fatalf("height-aware header should keep dir line, got:\n%s", plain)
	}
}

func TestRenderStatusHeaderDoesNotPaintInlineHeaderBlocks(t *testing.T) {
	withTrueColorProfile(t)

	theme := chatTheme{
		AppBG:           lipgloss.Color("#112233"),
		HeaderBG:        lipgloss.Color("#445566"),
		HeaderFG:        lipgloss.Color("#ddeeff"),
		AccentPrimary:   lipgloss.Color("#88aaff"),
		AccentSecondary: lipgloss.Color("#ffcc66"),
		Text:            lipgloss.Color("#eef2f7"),
		TextDim:         lipgloss.Color("#8b97a8"),
		Border:          lipgloss.Color("#334455"),
	}
	rendered := renderStatusHeader(theme, chatStatusData{
		Model:   "openai/gpt-5",
		WorkDir: "/tmp/work",
	}, 80)

	if strings.Contains(rendered, ansiBackgroundFragment(theme.HeaderBG)) {
		t.Fatalf("header should not paint inline background blocks: %q", rendered)
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

func TestRenderStatusHeaderUsesSplitRailCardAtWideWidths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}

	rendered := renderStatusHeader(lookupThemeForTest(t, "default"), chatStatusData{
		Model:   "openai/gpt-5.4",
		WorkDir: home + "/Documents/OPC/git/other/forge",
	}, 80)

	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 {
		t.Fatalf("header lines = %d, want at least 3: %q", len(lines), rendered)
	}
	headerLines := make([]string, 0, 3)
	for _, line := range lines[:3] {
		headerLines = append(headerLines, strippedLine(line))
	}
	header := strings.Join(headerLines, "\n")
	for _, want := range []string{"FORGE", "openai/gpt-5.4", "model", "dir", "~/Documents/OPC/git/other/forge"} {
		if !strings.Contains(header, want) {
			t.Fatalf("wide header missing %q in:\n%s", want, header)
		}
	}
	for _, want := range []string{"▌ model", "▌ dir"} {
		if !strings.Contains(header, want) {
			t.Fatalf("wide header missing rail label %q in:\n%s", want, header)
		}
	}
	if strings.ContainsAny(header, "╭╮╰╯") {
		t.Fatalf("header should be flat (no box border), got:\n%s", header)
	}
	for _, line := range headerLines {
		if ansiPrintableWidth(line) > 80 {
			t.Fatalf("wide header line exceeds width: %d for %q", ansiPrintableWidth(line), line)
		}
	}
}

func TestRenderForgeWordmarkUsesSingleAccentColor(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	wordmark := renderForgeWordmark(theme)

	for _, letter := range []string{"F", "O", "R", "G", "E"} {
		assertStyledSubstring(t, wordmark, letter, theme.AccentPrimary)
	}
}

func TestRenderStatusHeaderFallsBackAcrossWidths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}

	theme := lookupThemeForTest(t, "default")
	data := chatStatusData{
		Model:   "openai/gpt-5.4-super-long-preview-build-with-extra-suffix-and-more",
		WorkDir: home + "/Documents/OPC/git/other/forge/internal/tui/very/long/path/with/many/segments",
	}

	mediumRaw := strings.Split(renderStatusHeader(theme, data, 56), "\n")
	narrowRaw := strings.Split(renderStatusHeader(theme, data, 48), "\n")
	medium := make([]string, 0, len(mediumRaw))
	for _, line := range mediumRaw {
		medium = append(medium, strippedLine(line))
	}
	narrow := make([]string, 0, len(narrowRaw))
	for _, line := range narrowRaw {
		narrow = append(narrow, strippedLine(line))
	}
	mediumHeader := strings.Join(medium, "\n")
	narrowHeader := strings.Join(narrow, "\n")

	if !strings.Contains(mediumHeader, "FORGE") {
		t.Fatalf("medium header should keep collapsed wordmark:\n%s", mediumHeader)
	}
	if strings.Contains(narrowHeader, "FORGE") {
		t.Fatalf("narrow header should hide ASCII mark:\n%s", narrowHeader)
	}
	if !strings.Contains(mediumHeader, "…") || !strings.Contains(narrowHeader, "…") {
		t.Fatalf("expected ellipsis truncation in medium/narrow headers:\nmedium=%s\nnarrow=%s", mediumHeader, narrowHeader)
	}
	for _, line := range medium {
		if ansiPrintableWidth(line) > 56 {
			t.Fatalf("medium header line exceeds width: %d for %q", ansiPrintableWidth(line), line)
		}
	}
	for _, line := range narrow {
		if ansiPrintableWidth(line) > 48 {
			t.Fatalf("narrow header line exceeds width: %d for %q", ansiPrintableWidth(line), line)
		}
	}
}

func TestRenderStatusHeaderPaintsFullRowWidth(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	rendered := renderStatusHeader(theme, chatStatusData{
		Model:   "openai/gpt-5.4",
		WorkDir: "/tmp/work",
	}, 120)

	for idx, line := range strings.Split(rendered, "\n") {
		if ansiPrintableWidth(line) != 120 {
			t.Fatalf("line %d width = %d, want 120: %q", idx+1, ansiPrintableWidth(line), strippedLine(line))
		}
	}
}

func TestRenderStatusHeaderUsesHeaderSurfaceAcrossAllThemes(t *testing.T) {
	withTrueColorProfile(t)

	for _, theme := range orderedChatThemes() {
		rendered := renderStatusHeader(theme, chatStatusData{
			Model:   "openai/gpt-5.4",
			WorkDir: "/tmp/work",
		}, 96)
		plain := strippedLine(rendered)
		if strings.ContainsAny(plain, "╭╮╰╯│") {
			t.Fatalf("theme %q rendered boxed/separator glyphs in header:\n%s", theme.ID, plain)
		}
		if !strings.Contains(plain, "▌ model") || !strings.Contains(plain, "▌ dir") {
			t.Fatalf("theme %q should keep the strengthened rail layout:\n%s", theme.ID, plain)
		}
		for idx, line := range strings.Split(rendered, "\n") {
			if ansiPrintableWidth(line) != 96 {
				t.Fatalf("theme %q line %d width = %d, want 96: %q", theme.ID, idx+1, ansiPrintableWidth(line), strippedLine(line))
			}
		}
	}
}
