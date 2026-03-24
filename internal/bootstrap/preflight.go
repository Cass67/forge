package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"forge/internal/auth"
	"forge/internal/config"
	"forge/internal/llm"
	"forge/internal/tui"
)

type Issue struct {
	Name     string
	OK       bool
	Detail   string
	Severity string
}

func Preflight(cfg *config.Config, tokens *auth.Tokens, reg *llm.Registry) []Issue {
	issues := make([]Issue, 0)
	appendKeyIssue := func(name, detail string, ok bool) {
		issues = append(issues, Issue{Name: name, OK: ok, Detail: detail, Severity: severity(ok)})
	}

	models := []string{cfg.Models.Writer, cfg.Models.Auditor, cfg.Models.Summarizer}
	needsAnthropic := false
	needsClaude := false
	needsEitherClaude := false
	for _, model := range models {
		ref := ParseModelRef(model)
		switch {
		case ref.Provider == "anthropic":
			needsAnthropic = true
		case ref.Provider == "claude":
			needsClaude = true
		case ref.Provider == "" && IsAnthropicModel(model):
			needsEitherClaude = true
		}
	}
	needsOpenAI := IsOpenAIModel(cfg.Models.Writer) || IsOpenAIModel(cfg.Models.Auditor) || IsOpenAIModel(cfg.Models.Summarizer)
	needsChatGPT := usesChatGPTProvider(cfg.Models.Writer) || usesChatGPTProvider(cfg.Models.Auditor) || usesChatGPTProvider(cfg.Models.Summarizer)

	if needsAnthropic {
		appendKeyIssue("ANTHROPIC_API_KEY", "not set — add in Forge provider auth or export in shell", cfg.AnthropicKey() != "")
	}
	if needsClaude {
		appendKeyIssue("Claude auth", "not found — sign in with Forge to authorize Claude.ai", claudeAuthAvailable())
	}
	if needsEitherClaude {
		appendKeyIssue("Claude model auth", "not set — sign in with Forge for Claude.ai or configure an Anthropic API key", claudeAuthAvailable() || cfg.AnthropicKey() != "")
	}
	if needsOpenAI {
		appendKeyIssue("OPENAI_API_KEY", "not set — add in Forge provider auth or export in shell", cfg.OpenAIKey() != "")
	}
	if needsChatGPT {
		appendKeyIssue("ChatGPT auth", "not found — sign in with Forge to authorize ChatGPT", chatGPTAuthAvailable())
	}

	compatEnvVars := map[string]string{
		"groq":       "GROQ_API_KEY",
		"mistral":    "MISTRAL_API_KEY",
		"xai":        "XAI_API_KEY",
		"nvidia":     "NVIDIA_API_KEY",
		"openrouter": "OPENROUTER_API_KEY",
		"together":   "TOGETHER_AI_API_KEY",
		"perplexity": "PERPLEXITY_API_KEY",
		"deepinfra":  "DEEPINFRA_API_KEY",
		"cerebras":   "CEREBRAS_API_KEY",
	}
	seen := map[string]bool{}
	for _, m := range []string{cfg.Models.Writer, cfg.Models.Auditor, cfg.Models.Summarizer} {
		if cp, ambiguous := ResolveCompatProvider(BuildCompatProviders(cfg, tokens), m); cp != nil && !seen[cp.Name] {
			seen[cp.Name] = true
			appendKeyIssue(compatEnvVars[cp.Name], "not set — add in Forge provider auth or export in shell", cp.KeyFn() != "")
		} else if ambiguous {
			issues = append(issues, Issue{Name: m, OK: false, Detail: "model matches multiple configured compatible providers; use an explicit provider prefix", Severity: "error"})
		}
	}

	needsCopilot := IsCopilotModel(cfg.Models.Writer) || IsCopilotModel(cfg.Models.Auditor) || IsCopilotModel(cfg.Models.Summarizer)
	if needsCopilot {
		appendKeyIssue("GitHub Copilot", "run: forge auth copilot", tokens.CopilotToken != "")
	}

	for _, model := range []string{cfg.Models.Writer, cfg.Models.Auditor, cfg.Models.Summarizer} {
		if model == "" {
			issues = append(issues, Issue{Name: "model", OK: false, Detail: "configured model name is empty", Severity: "error"})
			continue
		}
		if _, err := reg.Lookup(model); err != nil && DriverForModel(cfg, tokens, model) == nil {
			issues = append(issues, Issue{Name: model, OK: false, Detail: fmt.Sprintf("model is not resolvable with current credentials: %v", err), Severity: "error"})
		}
	}

	if err := os.MkdirAll(cfg.Session.OutputDir, 0o755); err != nil {
		issues = append(issues, Issue{Name: "output", OK: false, Detail: fmt.Sprintf("cannot create output dir: %v", err), Severity: "error"})
	} else {
		testFile := filepath.Join(cfg.Session.OutputDir, ".forge-write-check")
		if err := os.WriteFile(testFile, []byte("ok"), 0o644); err != nil {
			issues = append(issues, Issue{Name: "output", OK: false, Detail: fmt.Sprintf("output dir is not writable: %v", err), Severity: "error"})
		} else {
			_ = os.Remove(testFile)
		}
	}

	return issues
}

func usesChatGPTProvider(model string) bool {
	return ParseModelRef(model).Provider == "chatgpt"
}

func SendStartupChecks(p *tea.Program, issues []Issue) {
	failed := false
	for _, issue := range issues {
		if !issue.OK {
			failed = true
		}
		p.Send(tui.CheckResult{Name: issue.Name, OK: issue.OK, Detail: detailFor(issue)})
	}
	if !failed {
		p.Send(tui.StartupComplete{})
	}
}

func severity(ok bool) string {
	if ok {
		return "info"
	}
	return "error"
}

func detailFor(issue Issue) string {
	if issue.OK {
		return ""
	}
	return issue.Detail
}
