package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"forge/internal/auth"
	"forge/internal/config"
	"forge/internal/copilot"
	"forge/internal/llm"
	"forge/internal/llm/drivers"
	"forge/internal/output"
	"forge/internal/prompts"
	"forge/internal/session"
	"forge/internal/summarizer"
	"forge/internal/tui"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "auth" && os.Args[2] == "copilot" {
		runCopilotAuth()
		return
	}

	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "forge", "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	tokens, err := auth.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load auth store: %v\n", err)
		tokens = &auth.Tokens{}
	}

	reg := llm.NewRegistry()
	registered := map[string]bool{}
	registerOnce := func(d llm.Driver) {
		if !registered[d.Name()] {
			reg.Register(d)
			registered[d.Name()] = true
		}
	}
	if key := cfg.AnthropicKey(); key != "" {
		for _, m := range []string{cfg.Models.Writer, cfg.Models.Auditor, cfg.Models.Summarizer} {
			if isAnthropicModel(m) {
				registerOnce(drivers.NewClaude(key, m))
			}
		}
	}
	if key := cfg.OpenAIKey(); key != "" {
		for _, m := range []string{cfg.Models.Writer, cfg.Models.Auditor, cfg.Models.Summarizer} {
			if isOpenAIModel(m) {
				registerOnce(drivers.NewOpenAI(key, m))
			}
		}
	}
	compatProvs := buildCompatProviders(cfg)
	for _, m := range []string{cfg.Models.Writer, cfg.Models.Auditor, cfg.Models.Summarizer} {
		if p := findCompatProvider(compatProvs, m); p != nil {
			registerOnce(drivers.NewOpenAICompatible(p.keyFn(), p.baseURL, m))
		}
	}
	if tokens.CopilotToken != "" {
		for _, m := range []string{cfg.Models.Writer, cfg.Models.Auditor, cfg.Models.Summarizer} {
			if isCopilotModel(m) {
				registerOnce(drivers.NewCopilot(tokens.CopilotToken, m, copilotAPIModel(m)))
			}
		}
	}

	available := availableModels(cfg, tokens)
	app := tui.NewApp(tui.AppConfig{
		WriterModels:   available,
		AuditorModels:  available,
		DefaultWriter:  cfg.Models.Writer,
		DefaultAuditor: cfg.Models.Auditor,
	})

	p := tea.NewProgram(app, tea.WithAltScreen())
	go runStartupChecks(cfg, tokens, reg, p)
	retModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	finalApp, _ := retModel.(tui.App)
	if !finalApp.Started {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := session.NewTurnGate()

	events, outDir := startSession(ctx, cfg, tokens, reg, finalApp.LastStart(), gate, "")

	lastStart := finalApp.LastStart()
	for {
		result := tui.RunLive(events, 4, lastStart.Rounds, tui.LiveConfig{
			WriterModel:  lastStart.WriterModel,
			AuditorModel: lastStart.AuditorModel,
			Gate:         gate,
		}, outDir)

		aborted := result.Aborted
		reason := ""
		if result.Err != nil {
			reason = result.Err.Error()
		}

		post := tui.RunPostSession(outDir, aborted, reason, lastStart, available, available)
		if !post.Fix {
			break
		}

		lastStart = tui.SessionStarted{
			Prompt:       post.Issue,
			WriterModel:  post.WriterModel,
			AuditorModel: post.AuditorModel,
			Rounds:       lastStart.Rounds,
			LangHint:     lastStart.LangHint,
		}
		gate = session.NewTurnGate() // fresh gate — don't carry manual-mode state across sessions
		events, outDir = startSession(ctx, cfg, tokens, reg, lastStart, gate, filepath.Join(outDir, "code"))
	}
}

func startSession(ctx context.Context, cfg *config.Config, tokens *auth.Tokens, reg *llm.Registry, started tui.SessionStarted, gate *session.TurnGate, seedFrom string) (<-chan llm.Event, string) {
	events := make(chan llm.Event, 256)

	w, err := output.NewWriter(cfg.Session.OutputDir, time.Now())
	if err != nil {
		go func() {
			events <- llm.Event{Kind: llm.EventAbort, Err: err}
			close(events)
		}()
		return events, ""
	}

	if seedFrom != "" {
		if err := w.SeedFrom(seedFrom); err != nil {
			go func() {
				events <- llm.Event{Kind: llm.EventAbort, Err: fmt.Errorf("seed from prior output: %w", err)}
				close(events)
			}()
			return events, w.Dir()
		}
	}

	go func() {
		// Register any custom model names chosen in the TUI that aren't already in the registry.
		ensureDriver(cfg, tokens, reg, started.WriterModel)
		ensureDriver(cfg, tokens, reg, started.AuditorModel)

		writerDriver, err := reg.Lookup(started.WriterModel)
		if err != nil {
			events <- llm.Event{Kind: llm.EventAbort, Err: err}
			close(events)
			return
		}
		auditorDriver, err := reg.Lookup(started.AuditorModel)
		if err != nil {
			events <- llm.Event{Kind: llm.EventAbort, Err: err}
			close(events)
			return
		}
		sumDriver, err := reg.Lookup(cfg.Models.Summarizer)
		if err != nil {
			events <- llm.Event{Kind: llm.EventAbort, Err: err}
			close(events)
			return
		}

		store := summarizer.NewStore(filepath.Join(w.Dir(), "summary-store.md"))
		passes := session.DefaultPasses(started.Rounds)
		passPrompts := []string{prompts.Correctness, prompts.Refactor, prompts.Security, prompts.Prod}
		auditorPrompts := []string{prompts.CorrectnessAuditor, prompts.RefactorAuditor, prompts.SecurityAuditor, prompts.ProdAuditor}

		runner := session.NewRunner(session.RunnerConfig{
			Passes:         passes,
			Writer:         writerDriver,
			Auditor:        auditorDriver,
			SumDriver:      sumDriver,
			SumPrompt:      prompts.Summarizer,
			AuditPrompt:    prompts.AuditLog,
			WriterW:        w,
			Store:          store,
			Events:         events,
			Gate:           gate,
			UserPrompt:     started.Prompt,
			LanguageHint:   started.LangHint,
			PassPrompts:    passPrompts,
			AuditorPrompts: auditorPrompts,
		})

		runner.Run(ctx)
	}()

	return events, w.Dir()
}

func runStartupChecks(cfg *config.Config, tokens *auth.Tokens, reg *llm.Registry, p *tea.Program) {
	failed := false

	checkKey := func(label, key string) {
		ok := key != ""
		detail := ""
		if !ok {
			detail = "not set — add to ~/.config/forge/config.toml or export in shell"
			failed = true
		}
		p.Send(tui.CheckResult{Name: label, OK: ok, Detail: detail})
	}

	needsAnthropic := isAnthropicModel(cfg.Models.Writer) ||
		isAnthropicModel(cfg.Models.Auditor) ||
		isAnthropicModel(cfg.Models.Summarizer)
	needsOpenAI := isOpenAIModel(cfg.Models.Writer) || isOpenAIModel(cfg.Models.Auditor) ||
		isOpenAIModel(cfg.Models.Summarizer)

	if needsAnthropic {
		checkKey("ANTHROPIC_API_KEY", cfg.AnthropicKey())
	}
	if needsOpenAI {
		checkKey("OPENAI_API_KEY", cfg.OpenAIKey())
	}

	// Check compat providers needed by configured models.
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
	provs := buildCompatProviders(cfg)
	for _, m := range []string{cfg.Models.Writer, cfg.Models.Auditor, cfg.Models.Summarizer} {
		if cp := findCompatProvider(provs, m); cp != nil && !seen[cp.name] {
			seen[cp.name] = true
			checkKey(compatEnvVars[cp.name], cp.keyFn())
		}
	}

	needsCopilot := isCopilotModel(cfg.Models.Writer) ||
		isCopilotModel(cfg.Models.Auditor) ||
		isCopilotModel(cfg.Models.Summarizer)
	if needsCopilot {
		ok := tokens.CopilotToken != ""
		detail := ""
		if !ok {
			detail = "run: forge auth copilot"
			failed = true
		}
		p.Send(tui.CheckResult{Name: "GitHub Copilot", OK: ok, Detail: detail})
	}

	if !failed {
		p.Send(tui.StartupComplete{})
	}
}

// compatProvider describes an OpenAI-compatible third-party provider.
type compatProvider struct {
	name    string
	baseURL string
	keyFn   func() string
	isModel func(string) bool
	models  []string
}

func buildCompatProviders(cfg *config.Config) []compatProvider {
	return []compatProvider{
		{
			name:    "xai",
			baseURL: "https://api.x.ai/v1",
			keyFn:   cfg.XAIKey,
			isModel: func(m string) bool { return strings.HasPrefix(m, "grok-") },
			models:  []string{"grok-3", "grok-3-mini", "grok-3-fast", "grok-2"},
		},
		{
			name:    "mistral",
			baseURL: "https://api.mistral.ai/v1",
			keyFn:   cfg.MistralKey,
			isModel: func(m string) bool {
				return strings.HasPrefix(m, "mistral-") ||
					strings.HasPrefix(m, "codestral-") ||
					strings.HasPrefix(m, "pixtral-") ||
					strings.HasPrefix(m, "magistral-")
			},
			models: []string{"mistral-large-latest", "mistral-small-latest", "codestral-latest", "pixtral-large-latest"},
		},
		{
			name:    "perplexity",
			baseURL: "https://api.perplexity.ai",
			keyFn:   cfg.PerplexityKey,
			isModel: func(m string) bool { return strings.HasPrefix(m, "sonar") },
			models:  []string{"sonar-pro", "sonar", "sonar-reasoning-pro", "sonar-reasoning"},
		},
		{
			name:    "cerebras",
			baseURL: "https://api.cerebras.ai/v1",
			keyFn:   cfg.CerebrasKey,
			// Cerebras uses llama3. (dot notation, no hyphen after digit)
			isModel: func(m string) bool { return strings.HasPrefix(m, "llama3.") },
			models:  []string{"llama3.1-8b", "llama3.3-70b"},
		},
		{
			name:    "groq",
			baseURL: "https://api.groq.com/openai/v1",
			keyFn:   cfg.GroqKey,
			isModel: func(m string) bool {
				return strings.HasPrefix(m, "llama-") ||
					strings.HasPrefix(m, "gemma") ||
					strings.HasPrefix(m, "mixtral") ||
					strings.HasPrefix(m, "qwen-") ||
					strings.HasPrefix(m, "compound") ||
					strings.HasPrefix(m, "deepseek-r1-")
			},
			models: []string{
				"llama-3.3-70b-versatile",
				"llama-3.1-8b-instant",
				"gemma2-9b-it",
				"mixtral-8x7b-32768",
				"qwen-qwq-32b",
				"deepseek-r1-distill-llama-70b",
			},
		},
		{
			name:    "nvidia",
			baseURL: "https://integrate.api.nvidia.com/v1",
			keyFn:   cfg.NVIDIAKey,
			isModel: func(m string) bool { return strings.HasPrefix(m, "nvidia/") },
			models:  []string{"nvidia/llama-3.1-nemotron-51b-instruct", "meta/llama-3.3-70b-instruct"},
		},
		// Providers that use namespaced model IDs (vendor/model-name).
		// Checked in order: Together → DeepInfra → OpenRouter.
		// If multiple are configured and a model matches, the first one wins.
		{
			name:    "together",
			baseURL: "https://api.together.xyz/v1",
			keyFn:   cfg.TogetherKey,
			isModel: func(m string) bool { return strings.Contains(m, "/") },
			models: []string{
				"meta-llama/Llama-3.3-70B-Instruct-Turbo",
				"deepseek-ai/DeepSeek-R1",
				"Qwen/QwQ-32B-Preview",
			},
		},
		{
			name:    "deepinfra",
			baseURL: "https://api.deepinfra.com/v1/openai",
			keyFn:   cfg.DeepInfraKey,
			isModel: func(m string) bool { return strings.Contains(m, "/") },
			models: []string{
				"meta-llama/Meta-Llama-3.1-8B-Instruct",
				"deepseek-ai/DeepSeek-R1-Turbo",
				"microsoft/WizardLM-2-8x22B",
			},
		},
		{
			name:    "openrouter",
			baseURL: "https://openrouter.ai/api/v1",
			keyFn:   cfg.OpenRouterKey,
			isModel: func(m string) bool { return strings.Contains(m, "/") },
			models: []string{
				"openai/gpt-4o",
				"anthropic/claude-sonnet-4-5",
				"meta-llama/llama-3.3-70b-instruct:free",
			},
		},
	}
}

// findCompatProvider returns the first compat provider whose key is set and
// whose isModel predicate matches the given model name.
func findCompatProvider(providers []compatProvider, model string) *compatProvider {
	for i := range providers {
		p := &providers[i]
		if p.keyFn() != "" && p.isModel(model) {
			return p
		}
	}
	return nil
}

func anthropicModels() []string {
	return []string{
		// 3.x — broadly accessible on all tiers
		"claude-3-7-sonnet-latest",
		"claude-3-7-sonnet-20250219",
		"claude-3-5-haiku-latest",
		"claude-3-5-haiku-20241022",
		// 4.5 — requires API access
		"claude-sonnet-4-5",
		"claude-sonnet-4-5-20250929",
		"claude-haiku-4-5",
		"claude-haiku-4-5-20251001",
		"claude-opus-4-5-20251101",
		// 4.6 — newest, requires API access
		"claude-sonnet-4-6",
		"claude-opus-4-6",
	}
}

func openAIModels() []string {
	return []string{
		"gpt-5.4",
		"gpt-5-mini",
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4-turbo",
		"o1",
		"o1-mini",
		"o3-mini",
	}
}

func allModels() []string {
	return append(anthropicModels(), openAIModels()...)
}

func isAnthropicModel(name string) bool {
	return strings.HasPrefix(name, "claude")
}

func isOpenAIModel(name string) bool {
	return strings.HasPrefix(name, "gpt") || strings.HasPrefix(name, "o1") || strings.HasPrefix(name, "o3")
}

// availableModels returns only models whose provider has a configured API key or token.
func availableModels(cfg *config.Config, tokens *auth.Tokens) []string {
	var out []string
	if cfg.AnthropicKey() != "" {
		out = append(out, anthropicModels()...)
	}
	if cfg.OpenAIKey() != "" {
		out = append(out, openAIModels()...)
	}
	for _, p := range buildCompatProviders(cfg) {
		if p.keyFn() != "" {
			out = append(out, p.models...)
		}
	}
	if tokens.CopilotToken != "" {
		out = append(out, copilotModels(tokens.CopilotToken)...)
	}
	if len(out) == 0 {
		out = allModels() // fallback so the list is never empty
	}
	return out
}

// ensureDriver registers a driver for model if it isn't already in the registry.
// Handles custom model names typed in the TUI that weren't in the startup list.
func ensureDriver(cfg *config.Config, tokens *auth.Tokens, reg *llm.Registry, model string) {
	if _, err := reg.Lookup(model); err == nil {
		return // already registered
	}
	if isAnthropicModel(model) {
		if key := cfg.AnthropicKey(); key != "" {
			reg.Register(drivers.NewClaude(key, model))
		}
		return
	}
	if isOpenAIModel(model) {
		if key := cfg.OpenAIKey(); key != "" {
			reg.Register(drivers.NewOpenAI(key, model))
		}
		return
	}
	if isCopilotModel(model) {
		if tokens.CopilotToken != "" {
			reg.Register(drivers.NewCopilot(tokens.CopilotToken, model, copilotAPIModel(model)))
		}
		return
	}
	if p := findCompatProvider(buildCompatProviders(cfg), model); p != nil {
		reg.Register(drivers.NewOpenAICompatible(p.keyFn(), p.baseURL, model))
	}
}

func isCopilotModel(name string) bool {
	return strings.HasPrefix(name, "copilot/")
}

// copilotAPIModel strips the "copilot/" prefix to get the bare model ID for the API.
func copilotAPIModel(name string) string {
	return strings.TrimPrefix(name, "copilot/")
}

// copilotModels merges the live Copilot /models response with forge's curated
// alias catalog so the TUI shows a broader, more stable model list.
func copilotModels(token string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, _ := copilot.DiscoverModels(ctx, token)
	return models
}

func runCopilotAuth() {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "forge", "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	clientID := cfg.CopilotClientID()
	if clientID == "" {
		fmt.Fprintln(os.Stderr, "error: no GitHub OAuth App client ID available")
		os.Exit(1)
	}

	ctx := context.Background()

	fmt.Println("Requesting device code from GitHub...")
	dc, err := copilot.RequestDeviceCode(ctx, clientID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n  Visit:  %s\n  Code:   %s\n\nWaiting for authorization...\n", dc.VerificationURI, dc.UserCode)

	token, err := copilot.PollForToken(ctx, clientID, dc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	tokens, err := auth.Load()
	if err != nil {
		tokens = &auth.Tokens{}
	}
	tokens.CopilotToken = token
	if err := auth.Save(tokens); err != nil {
		fmt.Fprintf(os.Stderr, "error saving token: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\nAuthenticated! Copilot models are now available.")
	fmt.Println("Use copilot/gpt-5, copilot/claude-sonnet-4.5, copilot/gemini-2.5-pro, etc. in your config.")
}
