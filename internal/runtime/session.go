package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"forge/internal/auth"
	"forge/internal/bootstrap"
	"forge/internal/config"
	"forge/internal/llm"
	"forge/internal/logger"
	"forge/internal/output"
	"forge/internal/prompts"
	"forge/internal/session"
	"forge/internal/summarizer"
	"forge/internal/tui"
)

func StartSession(ctx context.Context, cfg *config.Config, tokens *auth.Tokens, reg *llm.Registry, started tui.SessionStarted, gate *session.TurnGate, seedFrom string, tracker *llm.UsageTracker, feedbackChan chan string) (<-chan llm.Event, string) {
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
		bootstrap.EnsureDriver(cfg, tokens, reg, started.WriterModel)
		bootstrap.EnsureDriver(cfg, tokens, reg, started.AuditorModel)
		bootstrap.EnsureDriver(cfg, tokens, reg, cfg.Models.Summarizer)

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

		wrapRetry := func(d llm.Driver) llm.Driver {
			return llm.NewRetryDriverWithIdleTimeout(d,
				cfg.Retry.MaxAttempts,
				time.Duration(cfg.Retry.InitialWait)*time.Millisecond,
				time.Duration(cfg.Retry.MaxWait)*time.Millisecond,
				time.Duration(cfg.Retry.Timeout)*time.Second,
				time.Duration(cfg.Resilience.StreamIdleTimeoutMS)*time.Millisecond,
			)
		}
		writerDriver = wrapRetry(writerDriver)
		auditorDriver = wrapRetry(auditorDriver)
		sumDriver = wrapRetry(sumDriver)

		if c, ok := writerDriver.(llm.Configurable); ok {
			c.SetParams(llm.Params{
				MaxTokens:   cfg.Models.WriterParams.MaxTokens,
				Temperature: cfg.Models.WriterParams.Temperature,
			})
		}
		if c, ok := auditorDriver.(llm.Configurable); ok {
			c.SetParams(llm.Params{
				MaxTokens:   cfg.Models.AuditorParams.MaxTokens,
				Temperature: cfg.Models.AuditorParams.Temperature,
			})
		}

		logLevel := logger.LevelInfo
		switch cfg.Log.Level {
		case "debug":
			logLevel = logger.LevelDebug
		case "warn":
			logLevel = logger.LevelWarn
		case "error":
			logLevel = logger.LevelError
		}
		logPath := cfg.Log.File
		if logPath == "" {
			logPath = filepath.Join(w.Dir(), "session.log")
		}
		log, logErr := logger.NewFileLogger(logPath, logLevel)
		if logErr != nil {
			log = logger.Nop()
		}

		store := summarizer.NewStore(filepath.Join(w.Dir(), "summary-store.md"))

		var passes []session.Pass
		var passPrompts, auditorPrompts []string

		if len(cfg.Pipeline) > 0 {
			resolved, pErr := config.ResolvePipeline(cfg.Pipeline, builtinPrompts(), started.Rounds)
			if pErr != nil {
				events <- llm.Event{Kind: llm.EventAbort, Err: fmt.Errorf("pipeline config: %w", pErr)}
				close(events)
				return
			}
			for i, name := range resolved.Names {
				passes = append(passes, session.Pass{Name: name, Rounds: resolved.Rounds[i]})
			}
			passPrompts = resolved.WriterPrompts
			auditorPrompts = resolved.AuditorPrompts
		} else {
			passes = session.DefaultPasses(started.Rounds)
			passPrompts = []string{prompts.Correctness, prompts.Refactor, prompts.Security, prompts.Prod}
			auditorPrompts = []string{prompts.CorrectnessAuditor, prompts.RefactorAuditor, prompts.SecurityAuditor, prompts.ProdAuditor}
		}

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
			Log:            log,
			Tracker:        tracker,
			GitEnabled:     cfg.Git.Enabled,
			GitAutoCommit:  cfg.Git.AutoCommit,
			Interactive:    started.Interactive,
			FeedbackChan:   feedbackChan,
			MirrorDir:      started.WorkDir,
		})

		runner.Run(ctx)
	}()

	return events, w.Dir()
}

func builtinPrompts() map[string]config.BuiltinPrompt {
	return map[string]config.BuiltinPrompt{
		"correctness": {Writer: prompts.Correctness, Auditor: prompts.CorrectnessAuditor},
		"refactor":    {Writer: prompts.Refactor, Auditor: prompts.RefactorAuditor},
		"security":    {Writer: prompts.Security, Auditor: prompts.SecurityAuditor},
		"prod":        {Writer: prompts.Prod, Auditor: prompts.ProdAuditor},
	}
}
