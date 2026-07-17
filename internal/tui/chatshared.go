package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/auth"
	"forge/internal/chatstate"
	"forge/internal/codexusage"
	"forge/internal/copilot"
	"forge/internal/llm"
	"forge/internal/modelcatalog"
	"forge/internal/output"
	"forge/internal/skills"

	"github.com/muesli/reflow/wordwrap"
)

// CheckResult is sent as a Bubble Tea message when a startup check completes.
type CheckResult struct {
	Name   string
	OK     bool
	Detail string
}

// StartupComplete is sent when all startup checks pass.
type StartupComplete struct{}

// SessionStarted is emitted when the user presses enter to start a pipeline session
// in the legacy input flow, or when a pipeline session is started via /make.
type SessionStarted struct {
	Prompt       string
	WriterModel  string
	AuditorModel string
	Rounds       int
	LangHint     string
	ContextFiles []string
	Interactive  bool
	WorkDir      string // where final code files are mirrored on completion
}

type ChatLiveConfig struct {
	Model                 string
	WorkDir               string
	DebugLogPath          string
	SurfaceKind           ChatSurfaceKind
	DebugEnabled          bool
	AvailableModels       []string
	Providers             []ProviderOption
	RefreshModels         func() []string
	ProbeModels           func(currentModel string, available []string) []string
	RefreshProviders      func() []ProviderOption
	ContextFiles          []string
	SwitchModel           func(name string) (newModel string, err error)
	ClearHistory          func()
	ApprovalCh            <-chan tools.Action
	ResponseCh            chan<- bool
	Skills                []skills.Skill
	AutoSkillsMode        string
	State                 *chatstate.State
	CopilotClientID       string
	FetchLiveCopilotQuota func(context.Context) (*copilot.UserQuota, error)
	FetchCodexUsage       func(context.Context) (*codexusage.Snapshot, error)
	ModelInfo             func(model string) *modelcatalog.ModelInfo
	DescribeModel         func(model string) string
	RequestMode           func() string
	// NotifyNudge is called by the runtime when it wants to push a nudge update
	// to the TUI. The arguments map to SelectNudge(mode, taskOp, suggestedSkill).
	// When nil, nudges are not pushed from the runtime.
	NotifyNudge func(mode, taskOp, suggestedSkill string)
	// NotifyNudgeSink, if non-nil, is written by the bubbletea layer after it
	// wraps NotifyNudge with p.Send. The runtime goroutine reads through this
	// pointer so its own nudge calls also reach the TUI program.
	NotifyNudgeSink *func(string, string, string)
	// StartPipeline, if set, starts a pipeline session from within chat mode.
	// The prompt, writerModel, and auditorModel are provided by the /make command.
	// Pipeline events should be sent through the existing events channel.
	// Returns an error if the pipeline cannot be started.
	StartPipeline func(prompt, writerModel, auditorModel string, rounds int) error
	// LoadPipelineDefaults returns the saved default writer model, auditor model, and rounds
	// for pipeline mode. If no defaults are saved, returns empty strings and 0.
	LoadPipelineDefaults func() (writerModel, auditorModel string, rounds int)
	// SavePipelineDefaults persists the given writer model, auditor model, and rounds
	// as defaults for future pipeline sessions.
	SavePipelineDefaults func(writerModel, auditorModel string, rounds int)
}

type ProviderOption struct {
	ID           string
	Label        string
	Status       string
	DefaultModel string
}

type ChatSurfaceKind string

const (
	ChatSurfaceDefault ChatSurfaceKind = "default"
	ChatSurfaceDebug   ChatSurfaceKind = "debug"
)

type ChatLiveResult struct {
	Aborted bool
	Input   string
}

type chatSessionSnapshot struct {
	SavedAt          time.Time `json:"saved_at"`
	Model            string    `json:"model"`
	WorkDir          string    `json:"work_dir"`
	AgentBuf         string    `json:"agent_buf"`
	ToolsBuf         string    `json:"tools_buf"`
	InputBuf         string    `json:"input_buf"`
	InputPos         int       `json:"input_pos"`
	AgentScrl        int       `json:"agent_scrl"`
	ToolsScrl        int       `json:"tools_scrl"`
	LeftPaneWidth    int       `json:"left_pane_width"`
	ToolsVisible     *bool     `json:"tools_visible,omitempty"`
	FocusRight       bool      `json:"focus_right"`
	AgentFollow      bool      `json:"agent_follow"`
	ToolsFollow      bool      `json:"tools_follow"`
	SearchQuery      string    `json:"search_query"`
	SearchPane       string    `json:"search_pane"`
	SearchCurrent    int       `json:"search_current"`
	SearchMatches    []int     `json:"search_matches"`
	SearchLineStarts []int     `json:"search_line_starts"`
	ContextFiles     []string  `json:"context_files,omitempty"`
	Turn             int       `json:"turn"`
	SessionUsage     llm.Usage `json:"session_usage,omitempty"`
}

type chatSessionEntry struct {
	name    string
	path    string
	modTime time.Time
}

func (cfg ChatLiveConfig) SurfaceMode() SurfaceModeConfig {
	mode := SurfaceModeConfig{
		EnableBracketedPaste: true,
		EnableLiveRegion:     true,
		EnableMouseCapture:   true,
	}
	return mode
}

func RunChatLive(events <-chan llm.Event, cfg ChatLiveConfig, inputCh chan<- string, doneCh <-chan struct{}) ChatLiveResult {
	return RunChatLiveBubbleTea(events, cfg, inputCh, doneCh)
}

func boolPtr(v bool) *bool {
	return &v
}

func chatSessionsDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "forge", "chat-sessions"), nil
}

func chatSessionFile(name string) (string, error) {
	dir, err := chatSessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sanitizeChatSessionName(name)+".json"), nil
}

func sanitizeChatSessionName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, string(os.PathSeparator), "-")
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.Trim(name, ".-")
	if name == "" {
		return "session"
	}
	return name
}

func defaultChatSessionName() (string, error) {
	return "session-" + time.Now().Format("2006-01-02-15-04-05"), nil
}

func listChatSessions() ([]chatSessionEntry, error) {
	dir, err := chatSessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	sessions := make([]chatSessionEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var snapshot chatSessionSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		sessions = append(sessions, chatSessionEntry{
			name:    name,
			path:    path,
			modTime: info.ModTime(),
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].modTime.After(sessions[j].modTime)
	})
	return sessions, nil
}

func latestChatSessionName() (string, error) {
	sessions, err := listChatSessions()
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return "", fmt.Errorf("no saved sessions")
	}
	return sessions[0].name, nil
}

func renameChatSession(oldName, newName string) error {
	oldPath, err := chatSessionFile(oldName)
	if err != nil {
		return err
	}
	newPath, err := chatSessionFile(newName)
	if err != nil {
		return err
	}
	return os.Rename(oldPath, newPath)
}

func deleteChatSession(name string) error {
	path, err := chatSessionFile(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func formatSessionTimestamp(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func scrollbarThumb(trackTop, trackH, totalLines, visibleLines, scroll int) (int, int) {
	if trackH <= 0 {
		return trackTop, 0
	}
	if totalLines <= visibleLines || visibleLines <= 0 {
		return trackTop, trackH
	}
	thumbH := max(1, visibleLines*trackH/max(1, totalLines))
	if thumbH > trackH {
		thumbH = trackH
	}
	maxScroll := max(0, totalLines-visibleLines)
	thumbY := trackTop
	if trackH > thumbH && maxScroll > 0 {
		thumbY += (scroll * (trackH - thumbH)) / maxScroll
	}
	return thumbY, thumbH
}

func copyToClipboard(content string) error {
	cmds := [][]string{
		{"pbcopy"},
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
	}
	for _, parts := range cmds {
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.Stdin = strings.NewReader(content)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no clipboard command available")
}

func providerUsesAPIKey(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "chatgpt", "claude", "copilot":
		return false
	default:
		return true
	}
}

func setProviderToken(t *auth.Tokens, id, value string) {
	id = strings.ToLower(strings.TrimSpace(id))
	value = strings.TrimSpace(value)
	switch id {
	case "anthropic":
		t.AnthropicAPIKey = value
	case "openai":
		t.OpenAIAPIKey = value
	case "groq":
		t.GroqAPIKey = value
	case "mistral":
		t.MistralAPIKey = value
	case "xai":
		t.XAIAPIKey = value
	case "zai", "zai-coding-plan":
		t.ZAIAPIKey = value
	case "nvidia":
		t.NVIDIAAPIKey = value
	case "openrouter":
		t.OpenRouterAPIKey = value
	case "together":
		t.TogetherAPIKey = value
	case "perplexity":
		t.PerplexityAPIKey = value
	case "deepinfra":
		t.DeepInfraAPIKey = value
	case "cerebras":
		t.CerebrasAPIKey = value
	case "opencode", "opencode-go":
		t.OpenCodeAPIKey = value
	case "brave":
		t.BraveAPIKey = value
	default:
		if value == "" {
			t.ClearCustomProviderKey(id)
			return
		}
		t.SetCustomProviderKey(id, value)
	}
}

func clearProviderToken(t *auth.Tokens, id string) {
	id = strings.ToLower(strings.TrimSpace(id))
	switch id {
	case "chatgpt":
		t.ChatGPTAccessToken = ""
		t.ChatGPTRefreshToken = ""
		t.ChatGPTAccountID = ""
		t.ChatGPTExpiresAt = time.Time{}
	case "claude":
		t.ClaudeAccessToken = ""
		t.ClaudeRefreshToken = ""
		t.ClaudeExpiresAt = time.Time{}
	case "copilot":
		t.CopilotToken = ""
	default:
		if providerUsesAPIKey(id) {
			setProviderToken(t, id, "")
		}
	}
}

func providerHasStoredCredential(t *auth.Tokens, id string) bool {
	if t == nil {
		return false
	}
	id = strings.ToLower(strings.TrimSpace(id))
	switch id {
	case "chatgpt":
		return strings.TrimSpace(t.ChatGPTAccessToken) != "" || strings.TrimSpace(t.ChatGPTRefreshToken) != ""
	case "claude":
		return strings.TrimSpace(t.ClaudeAccessToken) != "" || strings.TrimSpace(t.ClaudeRefreshToken) != ""
	case "copilot":
		return strings.TrimSpace(t.CopilotToken) != ""
	case "anthropic":
		return strings.TrimSpace(t.AnthropicAPIKey) != ""
	case "openai":
		return strings.TrimSpace(t.OpenAIAPIKey) != ""
	case "groq":
		return strings.TrimSpace(t.GroqAPIKey) != ""
	case "mistral":
		return strings.TrimSpace(t.MistralAPIKey) != ""
	case "xai":
		return strings.TrimSpace(t.XAIAPIKey) != ""
	case "nvidia":
		return strings.TrimSpace(t.NVIDIAAPIKey) != ""
	case "openrouter":
		return strings.TrimSpace(t.OpenRouterAPIKey) != ""
	case "together":
		return strings.TrimSpace(t.TogetherAPIKey) != ""
	case "perplexity":
		return strings.TrimSpace(t.PerplexityAPIKey) != ""
	case "deepinfra":
		return strings.TrimSpace(t.DeepInfraAPIKey) != ""
	case "cerebras":
		return strings.TrimSpace(t.CerebrasAPIKey) != ""
	case "opencode", "opencode-go":
		return strings.TrimSpace(t.OpenCodeAPIKey) != ""
	case "brave":
		return strings.TrimSpace(t.BraveAPIKey) != ""
	default:
		if !providerUsesAPIKey(id) {
			return false
		}
		return strings.TrimSpace(t.CustomProviderKey(id)) != ""
	}
}

// clamp constrains v to the [low, high] range.
func clamp(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

// fitCell truncates or pads s to exactly width runes.
func fitCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) > width {
		runes = runes[:width]
	}
	if len(runes) < width {
		return string(runes) + strings.Repeat(" ", width-len(runes))
	}
	return string(runes)
}

// wrapPlain wraps text to a given width and returns lines.
func wrapPlain(s string, width int) []string {
	if width < 1 {
		return []string{""}
	}
	wrapped := wordwrap.String(s, width)
	return strings.Split(wrapped, "\n")
}

// foldForDisplay replaces code blocks with a summary line for pipeline display.
func foldForDisplay(text string) string {
	blocks := output.ParseCodeBlocks(text)
	if len(blocks) == 0 {
		return text
	}

	lines := strings.Split(text, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		if !strings.HasPrefix(line, "```") {
			out = append(out, line)
			i++
			continue
		}

		header := strings.TrimPrefix(line, "```")
		colon := strings.Index(header, ":")
		if colon < 0 {
			out = append(out, line)
			i++
			continue
		}

		filename := header[colon+1:]
		i++
		codeLines := 0
		for i < len(lines) && lines[i] != "```" {
			codeLines++
			i++
		}
		if i < len(lines) && lines[i] == "```" {
			i++
		}
		if filename == "" {
			filename = "unnamed"
		}
		out = append(out, fmt.Sprintf("[code: %s %d lines]", filename, codeLines))
	}

	return strings.TrimSpace(strings.Join(out, "\n"))
}
