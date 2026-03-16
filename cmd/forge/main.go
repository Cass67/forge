package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"forge/internal/config"
	"forge/internal/llm"
	"forge/internal/llm/drivers"
	"forge/internal/output"
	"forge/internal/prompts"
	"forge/internal/session"
	"forge/internal/summarizer"
	"forge/internal/tui"
)

func main() {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "forge", "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	reg := llm.NewRegistry()
	if key := cfg.AnthropicKey(); key != "" {
		reg.Register(drivers.NewClaude(key, cfg.Models.Writer))
		if cfg.Models.Summarizer != cfg.Models.Writer {
			reg.Register(drivers.NewClaude(key, cfg.Models.Summarizer))
		}
	}
	if key := cfg.OpenAIKey(); key != "" {
		reg.Register(drivers.NewOpenAI(key, cfg.Models.Auditor))
	}

	app := tui.NewApp(tui.AppConfig{
		WriterModels:  anthropicModels(),
		AuditorModels: openAIModels(),
		OnStart: func(started tui.SessionStarted) (<-chan llm.Event, string) {
			return startSession(cfg, reg, started)
		},
	})

	p := tea.NewProgram(app, tea.WithAltScreen())
	go runStartupChecks(cfg, reg, p)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func startSession(cfg *config.Config, reg *llm.Registry, started tui.SessionStarted) (<-chan llm.Event, string) {
	events := make(chan llm.Event, 256)

	w, err := output.NewWriter(cfg.Session.OutputDir, time.Now())
	if err != nil {
		go func() {
			events <- llm.Event{Kind: llm.EventAbort, Err: err}
			close(events)
		}()
		return events, ""
	}

	go func() {
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

		runner := session.NewRunner(session.RunnerConfig{
			Passes:      passes,
			Writer:      writerDriver,
			Auditor:     auditorDriver,
			SumDriver:   sumDriver,
			SumPrompt:   prompts.Summarizer,
			AuditPrompt: prompts.AuditLog,
			WriterW:     w,
			Store:       store,
			Events:      events,
			UserPrompt:  started.Prompt,
			PassPrompts: passPrompts,
		})

		runner.Run(context.Background())
	}()

	return events, w.Dir()
}

func runStartupChecks(cfg *config.Config, reg *llm.Registry, p *tea.Program) {
	failed := false

	checkKey := func(name, key string) {
		ok := key != ""
		detail := ""
		if !ok {
			detail = "not set"
			failed = true
		}
		p.Send(tui.CheckResult{Name: name, OK: ok, Detail: detail})
	}

	anthropicKey := cfg.AnthropicKey()
	openAIKey := cfg.OpenAIKey()
	checkKey("ANTHROPIC_API_KEY", anthropicKey)
	checkKey("OPENAI_API_KEY", openAIKey)

	if failed {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if d, err := reg.Lookup(cfg.Models.Writer); err == nil {
		out := make(chan llm.Token, 4)
		msgs := []llm.Message{{Role: llm.RoleUser, Content: "ping"}}
		pingErr := d.Stream(ctx, msgs, out)
		for range out {}
		ok := pingErr == nil
		detail := ""
		if !ok {
			detail = pingErr.Error()
			failed = true
		}
		p.Send(tui.CheckResult{Name: cfg.Models.Writer + " ping", OK: ok, Detail: detail})
	}

	if !failed {
		if d, err := reg.Lookup(cfg.Models.Auditor); err == nil {
			out2 := make(chan llm.Token, 4)
			msgs2 := []llm.Message{{Role: llm.RoleUser, Content: "ping"}}
			pingErr2 := d.Stream(ctx, msgs2, out2)
			for range out2 {}
			ok2 := pingErr2 == nil
			detail2 := ""
			if !ok2 {
				detail2 = pingErr2.Error()
				failed = true
			}
			p.Send(tui.CheckResult{Name: cfg.Models.Auditor + " ping", OK: ok2, Detail: detail2})
		}
	}

	if !failed {
		p.Send(tui.StartupComplete{})
	}
}

func anthropicModels() []string {
	return []string{
		"claude-sonnet-4-6",
		"claude-opus-4-6",
		"claude-haiku-4-5-20251001",
	}
}

func openAIModels() []string {
	return []string{
		"gpt-4o",
		"gpt-4-turbo",
		"gpt-4o-mini",
	}
}
