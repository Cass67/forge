package tui

import (
	"fmt"
	"sort"
	"strings"

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
	LastUsage    llm.Usage
	SessionUsage llm.Usage
	RequestMode  string
	ContextUsed  int
	ContextLimit int
	CopilotLive  *copilot.UserQuota
	CodexUsage   *codexusage.Snapshot
	ModelInfo    *modelcatalog.ModelInfo
}

func buildStatusLine1(data chatStatusData) string {
	parts := []string{"forge"}
	if model := strings.TrimSpace(data.Model); model != "" {
		parts = append(parts, model)
	}
	if workDir := strings.TrimSpace(data.WorkDir); workDir != "" {
		parts = append(parts, workDir)
	}
	if theme := strings.TrimSpace(data.ThemeID); theme != "" {
		parts = append(parts, "theme: "+theme)
	}
	return strings.Join(parts, " • ")
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
	parts := make([]string, 0, 2)
	switch {
	case data.ContextLimit > 0:
		parts = append(parts, fmt.Sprintf("ctx %d/%d", data.ContextUsed, data.ContextLimit))
	case data.ContextUsed > 0:
		parts = append(parts, fmt.Sprintf("ctx %d", data.ContextUsed))
	}
	if mode := strings.TrimSpace(data.RequestMode); mode != "" {
		parts = append(parts, mode)
	}
	return strings.Join(parts, " • ")
}

func renderStatusHeader(theme chatTheme, data chatStatusData, width int) string {
	headerStyle := lipgloss.NewStyle().
		Background(theme.HeaderBG).
		Foreground(theme.HeaderFG).
		Width(width).
		Bold(true)

	line1 := fitCell(buildStatusLine1(data), width)
	line2 := fitCell(buildStatusLine2(data), width)

	return lipgloss.JoinVertical(lipgloss.Left,
		headerStyle.Render(line1),
		headerStyle.Render(line2),
	)
}

func (m *ChatModel) syncStatusData() {
	m.statusData.Model = m.model
	m.statusData.ThemeID = m.themeID
	m.statusData.WorkDir = m.workDir
	m.statusData.Status = m.status
	m.statusData.LastUsage = m.statsUsage
	m.statusData.SessionUsage = m.sessionUsage
	if m.config.RequestMode != nil {
		m.statusData.RequestMode = strings.TrimSpace(m.config.RequestMode())
	}
	if m.config.ModelInfo != nil {
		m.statusData.ModelInfo = m.config.ModelInfo(m.model)
	}
}

func (m ChatModel) statusSnapshot() chatStatusData {
	data := m.statusData
	data.Model = m.model
	data.ThemeID = m.themeID
	data.WorkDir = m.workDir
	data.Status = m.status
	data.LastUsage = m.statsUsage
	data.SessionUsage = m.sessionUsage
	if m.config.RequestMode != nil {
		data.RequestMode = strings.TrimSpace(m.config.RequestMode())
	}
	if m.config.ModelInfo != nil {
		data.ModelInfo = m.config.ModelInfo(m.model)
	}
	return data
}

func buildTurnSummary(usage llm.Usage) string {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return ""
	}
	return fmt.Sprintf("turn %d/%d", usage.InputTokens, usage.OutputTokens)
}

func buildSessionSummary(usage llm.Usage) string {
	total := usage.InputTokens + usage.OutputTokens
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("session %d", total)
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
