package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"forge/internal/codexusage"
	"forge/internal/copilot"
	"forge/internal/llm"
	"forge/internal/modelcatalog"

	"github.com/charmbracelet/lipgloss"
)

type chatStatusData struct {
	Model        string
	ThemeID      string
	WorkDir      string
	Status       string
	Duration     time.Duration
	LastUsage    llm.Usage
	SessionUsage llm.Usage
	RequestMode  string
	ContextUsed  int
	ContextLimit int
	CopilotLive  *copilot.UserQuota
	CodexUsage   *codexusage.Snapshot
	ModelInfo    *modelcatalog.ModelInfo
}

type chatStatsData struct {
	chatStatusData
	CopilotLoading bool
	CopilotErr     string
	CodexLoading   bool
	CodexErr       string
}

type statsSection struct {
	Title string
	Lines []string
}

func buildStatusLine1(data chatStatusData) string {
	parts := []string{"forge"}
	if model := strings.TrimSpace(data.Model); model != "" {
		parts = append(parts, model)
	}
	if workDir := strings.TrimSpace(data.WorkDir); workDir != "" {
		parts = append(parts, workDir)
	}
	return strings.Join(parts, " • ")
}

func (m ChatModel) statsSnapshot() chatStatsData {
	return chatStatsData{
		chatStatusData: m.statusSnapshot(),
		CopilotLoading: m.statsCopilotLoading,
		CopilotErr:     m.statsCopilotErr,
		CodexLoading:   m.statsCodexLoading,
		CodexErr:       m.statsCodexErr,
	}
}

func buildStatusLine2(data chatStatusData) string {
	parts := make([]string, 0, 5)
	if status := strings.TrimSpace(data.Status); status != "" {
		parts = append(parts, status)
	}
	if turn := buildTurnSummary(data.LastUsage); turn != "" {
		parts = append(parts, turn)
	}
	if session := buildSessionSummary(data.SessionUsage); session != "" {
		parts = append(parts, session)
	}
	if context := buildContextSummary(data); context != "" {
		parts = append(parts, context)
	}
	if provider := buildProviderStatusSummary(data); provider != "" {
		parts = append(parts, provider)
	}
	return strings.Join(parts, " • ")
}

func buildProviderStatusSummary(data chatStatusData) string {
	provider := strings.ToLower(strings.TrimSpace(providerFromModel(data.Model)))
	switch provider {
	case "copilot":
		if summary := buildCopilotQuotaSummary(data.CopilotLive); summary != "" {
			return summary
		}
		if summary := buildQuotaSummary(data.LastUsage.CopilotQuota); summary != "" {
			return summary
		}
		if summary := buildModelMetadataSummary(data.ModelInfo); summary != "" {
			return summary
		}
		return "copilot"
	case "chatgpt", "openai", "codex":
		if summary := buildCodexUsageSummary(data.CodexUsage); summary != "" {
			return summary
		}
		if summary := buildModelMetadataSummary(data.ModelInfo); summary != "" {
			return summary
		}
		if provider != "" {
			return provider
		}
		return ""
	default:
		if summary := buildModelMetadataSummary(data.ModelInfo); summary != "" {
			return summary
		}
		if provider != "" {
			return provider
		}
		return ""
	}
}

func buildContextSummary(data chatStatusData) string {
	used, limit := deriveContextUsage(data.SessionUsage, data.ModelInfo, data.ContextUsed, data.ContextLimit)
	parts := make([]string, 0, 2)
	switch {
	case limit > 0:
		parts = append(parts, fmt.Sprintf("est ctx %d/%d", used, limit))
	case used > 0:
		parts = append(parts, fmt.Sprintf("est ctx %d", used))
	}
	if mode := strings.TrimSpace(data.RequestMode); mode != "" {
		parts = append(parts, mode)
	}
	return strings.Join(parts, " • ")
}

func buildStatsSections(data chatStatsData) []statsSection {
	return []statsSection{
		{Title: "Turn", Lines: []string{buildTurnStatsLine(data)}},
		{Title: "Session", Lines: []string{buildSessionStatsLine(data)}},
		{Title: "Provider", Lines: []string{buildProviderStatsLine(data)}},
		{Title: "Model", Lines: []string{buildModelStatsLine(data)}},
		{Title: "Diagnostics", Lines: []string{buildDiagnosticsStatsLine(data)}},
	}
}

func renderStatsOverlayPanel(theme chatTheme, data chatStatsData, width, height int) string {
	if theme.ID == "" {
		theme, _ = lookupChatTheme("default")
	}

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	sectionTitleStyle := lipgloss.NewStyle().Foreground(theme.AccentSecondary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)

	sections := buildStatsSections(data)
	lines := []string{titleStyle.Render("Session stats"), ""}
	for idx, section := range sections {
		lines = append(lines, sectionTitleStyle.Render(section.Title))
		if len(section.Lines) == 0 {
			lines = append(lines, textStyle.Render("unavailable"))
		} else {
			for _, line := range section.Lines {
				lines = append(lines, textStyle.Render(line))
			}
		}
		if idx < len(sections)-1 {
			lines = append(lines, "")
		}
	}
	lines = append(lines, "", dimStyle.Render("Esc / Enter closes this overlay"))

	boxW := min(96, max(56, width-10))
	boxH := min(max(16, len(lines)+4), max(14, height-4))
	contentHeight := max(1, boxH-4)
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
	}
	content := lipgloss.NewStyle().
		Width(max(1, boxW-6)).
		Height(contentHeight).
		Render(strings.Join(lines, "\n"))
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(contentHeight).
		Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func buildTurnStatsLine(data chatStatsData) string {
	parts := []string{}
	if data.Duration > 0 {
		parts = append(parts, fmt.Sprintf("duration %.1fs", data.Duration.Seconds()))
	} else {
		parts = append(parts, "duration unavailable")
	}
	parts = append(parts, fmt.Sprintf("%d in / %d out", data.LastUsage.InputTokens, data.LastUsage.OutputTokens))
	if summary := buildQuotaSummary(data.LastUsage.CopilotQuota); summary != "" {
		parts = append(parts, summary)
	}
	return joinStatsParts(parts...)
}

func buildSessionStatsLine(data chatStatsData) string {
	total := data.SessionUsage.InputTokens + data.SessionUsage.OutputTokens
	parts := []string{
		fmt.Sprintf("%d in / %d out", data.SessionUsage.InputTokens, data.SessionUsage.OutputTokens),
		fmt.Sprintf("total %d", total),
	}
	if summary := buildContextSummary(data.chatStatusData); summary != "" {
		parts = append(parts, summary)
	}
	return joinStatsParts(parts...)
}

func buildProviderStatsLine(data chatStatsData) string {
	provider := strings.ToLower(strings.TrimSpace(providerFromModel(data.Model)))
	switch provider {
	case "copilot":
		if data.CopilotLoading {
			return "Copilot • loading"
		}
		if summary := buildCopilotQuotaSummary(data.CopilotLive); summary != "" {
			return joinStatsParts("Copilot", summary)
		}
		if data.CopilotErr != "" {
			return joinStatsParts("Copilot", "unavailable", data.CopilotErr)
		}
		if summary := buildQuotaSummary(data.LastUsage.CopilotQuota); summary != "" {
			return joinStatsParts("Copilot", summary)
		}
		return joinStatsParts("Copilot", "unavailable")
	case "chatgpt", "openai", "codex":
		if data.CodexLoading {
			return "OpenAI/Codex • loading"
		}
		if summary := buildCodexUsageSummary(data.CodexUsage); summary != "" {
			return joinStatsParts("OpenAI/Codex", summary)
		}
		if data.CodexErr != "" {
			return joinStatsParts("OpenAI/Codex", "unavailable", data.CodexErr)
		}
		return joinStatsParts("OpenAI/Codex", "unavailable")
	default:
		if provider == "" {
			return "Provider unavailable"
		}
		if summary := buildProviderStatusSummary(data.chatStatusData); summary != "" {
			return joinStatsParts(provider, summary)
		}
		return provider
	}
}

func buildModelStatsLine(data chatStatsData) string {
	parts := []string{}
	if model := strings.TrimSpace(data.Model); model != "" {
		parts = append(parts, model)
	} else {
		parts = append(parts, "unavailable")
	}
	if summary := buildModelLimitsSummary(data.ModelInfo); summary != "" {
		parts = append(parts, summary)
	}
	if summary := buildModelMetadataSummary(data.ModelInfo); summary != "" {
		parts = append(parts, summary)
	}
	return joinStatsParts(parts...)
}

func buildDiagnosticsStatsLine(data chatStatsData) string {
	parts := []string{}
	if summary := buildContextSummary(data.chatStatusData); summary != "" {
		parts = append(parts, summary)
	}
	if workDir := strings.TrimSpace(data.WorkDir); workDir != "" {
		parts = append(parts, "workdir "+workDir)
	}
	if status := strings.TrimSpace(data.Status); status != "" {
		parts = append(parts, "status "+status)
	}
	return joinStatsParts(parts...)
}

func joinStatsParts(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			filtered = append(filtered, part)
		}
	}
	if len(filtered) == 0 {
		return "unavailable"
	}
	return strings.Join(filtered, " • ")
}

func renderStatusHeader(theme chatTheme, data chatStatusData, width int) string {
	headerStyle := lipgloss.NewStyle().
		Background(theme.HeaderBG).
		Foreground(theme.HeaderFG).
		Width(width).
		Bold(true)

	return headerStyle.Render(fitCell(buildStatusLine1(data), width))
}

func (m *ChatModel) syncStatusData() {
	m.statusData.Model = m.model
	m.statusData.ThemeID = m.themeID
	m.statusData.WorkDir = m.workDir
	m.statusData.Status = m.status
	m.statusData.Duration = m.statsDuration
	m.statusData.LastUsage = m.statsUsage
	m.statusData.SessionUsage = m.sessionUsage
	if m.config.RequestMode != nil {
		m.statusData.RequestMode = strings.TrimSpace(m.config.RequestMode())
	}
	if m.config.ModelInfo != nil {
		m.statusData.ModelInfo = m.config.ModelInfo(m.model)
	}
	m.statusData.ContextUsed, m.statusData.ContextLimit = deriveContextUsage(m.statusData.SessionUsage, m.statusData.ModelInfo, m.statusData.ContextUsed, m.statusData.ContextLimit)
}

func (m ChatModel) statusSnapshot() chatStatusData {
	data := m.statusData
	data.Model = m.model
	data.ThemeID = m.themeID
	data.WorkDir = m.workDir
	data.Status = m.status
	data.Duration = m.statsDuration
	data.LastUsage = m.statsUsage
	data.SessionUsage = m.sessionUsage
	if m.config.RequestMode != nil {
		data.RequestMode = strings.TrimSpace(m.config.RequestMode())
	}
	if m.config.ModelInfo != nil {
		data.ModelInfo = m.config.ModelInfo(m.model)
	}
	data.ContextUsed, data.ContextLimit = deriveContextUsage(data.SessionUsage, data.ModelInfo, data.ContextUsed, data.ContextLimit)
	return data
}

func deriveContextUsage(session llm.Usage, info *modelcatalog.ModelInfo, existingUsed, existingLimit int) (used, limit int) {
	used = existingUsed
	if approx := session.InputTokens + session.OutputTokens; approx > used {
		used = approx
	}
	limit = existingLimit
	if info != nil && info.ContextWindow > 0 {
		limit = info.ContextWindow
	}
	return used, limit
}

func buildTurnSummary(usage llm.Usage) string {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return ""
	}
	return fmt.Sprintf("last %d in / %d out", usage.InputTokens, usage.OutputTokens)
}

func buildSessionSummary(usage llm.Usage) string {
	total := usage.InputTokens + usage.OutputTokens
	return fmt.Sprintf("session %d tok", total)
}

func providerFromModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parts[0]))
}

func buildCopilotQuotaSummary(quota *copilot.UserQuota) string {
	if quota == nil || len(quota.Windows) == 0 {
		return ""
	}
	ordered := []string{"premium", "chat", "completions"}
	parts := make([]string, 0, len(ordered))
	seen := map[string]bool{}
	for _, name := range ordered {
		if window, ok := quota.Windows[name]; ok {
			if summary := formatQuotaWindow(name, window); summary != "" {
				parts = append(parts, summary)
			}
			seen[name] = true
		}
	}
	if len(parts) == 0 {
		keys := make([]string, 0, len(quota.Windows))
		for name := range quota.Windows {
			if !seen[name] {
				keys = append(keys, name)
			}
		}
		sort.Strings(keys)
		for _, name := range keys {
			if summary := formatQuotaWindow(name, quota.Windows[name]); summary != "" {
				parts = append(parts, summary)
				break
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	out := "Copilot " + strings.Join(parts, " • ")
	if reset := strings.TrimSpace(quota.ResetAt); reset != "" {
		out += " • reset " + reset
	}
	return out
}

func buildQuotaSummary(quota *llm.CopilotQuota) string {
	if quota == nil {
		return ""
	}
	summary := formatQuotaWindow(quota.Type, *quota)
	if summary == "" {
		return ""
	}
	return "Copilot " + summary
}

func formatQuotaWindow(name string, quota llm.CopilotQuota) string {
	label := strings.TrimSpace(quota.Type)
	if label == "" {
		label = strings.TrimSpace(name)
	}
	if label == "" {
		label = "quota"
	}
	switch {
	case quota.Remaining > 0 && quota.Included > 0:
		return fmt.Sprintf("%s %d left", label, quota.Remaining)
	case quota.Remaining > 0:
		return fmt.Sprintf("%s %d left", label, quota.Remaining)
	case quota.Included > 0:
		return fmt.Sprintf("%s %d used", label, quota.Used)
	case quota.PercentRemaining > 0:
		return fmt.Sprintf("%s %.0f%% left", label, quota.PercentRemaining)
	case quota.Unlimited:
		return fmt.Sprintf("%s unlimited", label)
	case quota.ResetAt != "":
		return fmt.Sprintf("%s reset %s", label, quota.ResetAt)
	default:
		return ""
	}
}

func buildCodexUsageSummary(snapshot *codexusage.Snapshot) string {
	if snapshot == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	if plan := strings.TrimSpace(snapshot.Plan); plan != "" {
		parts = append(parts, plan)
	}
	if window := formatUsageWindow("5h", snapshot.Primary); window != "" {
		parts = append(parts, window)
	}
	if window := formatUsageWindow("7d", snapshot.Secondary); window != "" {
		parts = append(parts, window)
	}
	if window := formatUsageWindow("review", snapshot.CodeReview); window != "" {
		parts = append(parts, window)
	}
	if len(parts) == 0 {
		return ""
	}
	return "Codex " + strings.Join(parts, " • ")
}

func formatUsageWindow(label string, window *codexusage.Window) string {
	if window == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if window.UsedPercent > 0 {
		parts = append(parts, fmt.Sprintf("%.0f%%", window.UsedPercent))
	}
	if reset := strings.TrimSpace(window.ResetIn); reset != "" {
		parts = append(parts, reset)
	} else if reset := strings.TrimSpace(window.ResetAt); reset != "" {
		parts = append(parts, reset)
	}
	if len(parts) == 0 {
		return ""
	}
	return label + " " + strings.Join(parts, " ")
}

func buildModelMetadataSummary(info *modelcatalog.ModelInfo) string {
	if info == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if info.Reasoning {
		parts = append(parts, "reasoning")
	}
	if info.Temperature {
		parts = append(parts, "temperature")
	}
	if info.ToolCall {
		parts = append(parts, "tools")
	}
	return strings.Join(parts, " • ")
}

func buildModelLimitsSummary(info *modelcatalog.ModelInfo) string {
	if info == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if info.ContextWindow > 0 {
		parts = append(parts, fmt.Sprintf("ctx %d", info.ContextWindow))
	}
	if info.OutputLimit > 0 {
		parts = append(parts, fmt.Sprintf("out %d", info.OutputLimit))
	}
	return strings.Join(parts, " • ")
}
