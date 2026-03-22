package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"forge/internal/gitutil"
	"forge/internal/llm"
	"forge/internal/logger"
	"forge/internal/output"
	"forge/internal/summarizer"
)

// RunnerConfig holds everything the runner needs to execute a session.
type RunnerConfig struct {
	Passes         []Pass
	Writer         llm.Driver
	Auditor        llm.Driver
	SumDriver      llm.Driver
	SumPrompt      string // per-round summarizer system prompt
	AuditPrompt    string // audit-log system prompt (used at end)
	WriterW        *output.Writer
	Store          *summarizer.Store
	Events         chan<- llm.Event
	Gate           *TurnGate
	UserPrompt     string
	LanguageHint   string
	ContextFiles   []string // paths of context files inlined in round 1
	PassPrompts    []string // writer system prompts per pass (indexed by pass order)
	AuditorPrompts []string // auditor system prompts per pass (indexed by pass order)
	Log            *logger.Logger
	Tracker        *llm.UsageTracker
	GitEnabled     bool
	GitAutoCommit  bool
	Interactive    bool
	FeedbackChan   chan string
}

// Runner orchestrates the full session: all passes and rounds.
type Runner struct {
	cfg RunnerConfig
}

func NewRunner(cfg RunnerConfig) *Runner {
	if cfg.Log == nil {
		cfg.Log = logger.Nop()
	}
	return &Runner{cfg: cfg}
}

// Run executes all passes. It sends events to cfg.Events and closes the channel when done.
func (r *Runner) Run(ctx context.Context) {
	defer close(r.cfg.Events)

	r.cfg.Log.Info("session started", map[string]any{
		"passes":  len(r.cfg.Passes),
		"writer":  r.cfg.Writer.Name(),
		"auditor": r.cfg.Auditor.Name(),
	})

	sumAgent := summarizer.NewAgent(r.cfg.SumDriver, r.cfg.SumPrompt)

	codeDir := filepath.Join(r.cfg.WriterW.Dir(), "code")
	if r.cfg.GitEnabled {
		if err := gitutil.Init(codeDir); err != nil {
			r.warn("git init failed", err, map[string]any{"dir": codeDir})
		} else if err := gitutil.CommitAll(codeDir, "initial: seed code"); err != nil {
			r.warn("initial git commit failed", err, map[string]any{"dir": codeDir})
		}
	}

	for passIdx, pass := range r.cfg.Passes {
		passNum := passIdx + 1
		lastAuditorTurn := ""

		writerPrompt := r.passPrompt(passIdx)
		auditorPrompt := r.auditorPrompt(passIdx)

		r.cfg.Events <- llm.Event{Kind: llm.EventPassStart, Pass: passNum, PassName: pass.Name}
		r.cfg.Log.Info("pass started", map[string]any{"pass": passNum, "name": pass.Name})

		for roundNum := 1; roundNum <= pass.Rounds; roundNum++ {
			if ctx.Err() != nil {
				r.cfg.Events <- llm.Event{Kind: llm.EventAbort, Err: ctx.Err()}
				return
			}

			r.cfg.Events <- llm.Event{Kind: llm.EventRoundStart, Pass: passNum, Round: roundNum}
			r.cfg.Log.Debug("round started", map[string]any{"pass": passNum, "round": roundNum})

			storeText, _ := r.cfg.Store.ReadAll()

			round := NewRound(
				r.cfg.Writer,
				r.cfg.Auditor,
				sumAgent,
				r.cfg.WriterW,
				r.cfg.Store,
				r.cfg.Events,
				r.cfg.Gate,
				r.cfg.Log,
				r.cfg.Tracker,
			)

			converged, err := round.Run(ctx, writerPrompt, auditorPrompt, passNum, roundNum,
				r.cfg.UserPrompt, r.cfg.LanguageHint, storeText, lastAuditorTurn)
			if err != nil {
				r.cfg.Log.Error("round failed", map[string]any{"pass": passNum, "round": roundNum, "error": err.Error()})
				r.cfg.Events <- llm.Event{Kind: llm.EventError, Pass: passNum, Round: roundNum, Err: err}
				return
			}
			lastAuditorTurn = round.LastAuditorTurn()

			if r.cfg.GitEnabled && r.cfg.GitAutoCommit {
				if err := gitutil.CommitAll(codeDir, fmt.Sprintf("pass %d round %d: %s", passNum, roundNum, pass.Name)); err != nil {
					r.warn("round git commit failed", err, map[string]any{"dir": codeDir, "pass": passNum, "round": roundNum})
				}
			}

			if converged {
				r.cfg.Log.Info("pass converged", map[string]any{"pass": passNum, "round": roundNum})
				break
			}
		}

		r.cfg.Events <- llm.Event{Kind: llm.EventPassEnd, Pass: passNum}

		// Interactive mode: request user feedback between passes
		if r.cfg.Interactive && r.cfg.FeedbackChan != nil && passIdx < len(r.cfg.Passes)-1 {
			r.cfg.Events <- llm.Event{Kind: llm.EventFeedbackRequest, Pass: passNum, PassName: pass.Name}
			select {
			case feedback := <-r.cfg.FeedbackChan:
				if feedback != "" {
					r.cfg.UserPrompt += "\n\nUSER FEEDBACK AFTER " + pass.Name + " PASS:\n" + feedback
					r.cfg.Log.Info("user feedback received", map[string]any{"pass": passNum, "len": len(feedback)})
				}
			case <-ctx.Done():
				r.cfg.Events <- llm.Event{Kind: llm.EventAbort, Err: ctx.Err()}
				return
			}
		}
	}

	// generate audit log
	storeText, _ := r.cfg.Store.ReadAll()
	auditAgent := summarizer.NewAgent(r.cfg.SumDriver, r.cfg.AuditPrompt)
	auditText, err := auditAgent.GenerateAuditLog(ctx, "", storeText)
	if err == nil {
		if writeErr := os.WriteFile(filepath.Join(r.cfg.WriterW.Dir(), "audit-log.md"), []byte(auditText), 0o644); writeErr != nil {
			r.warn("audit log write failed", writeErr, map[string]any{"dir": r.cfg.WriterW.Dir()})
		}
	} else {
		r.warn("audit log generation failed", err, nil)
	}

	if r.cfg.GitEnabled {
		if err := gitutil.CommitAll(codeDir, "final: session complete"); err != nil {
			r.warn("final git commit failed", err, map[string]any{"dir": codeDir})
		}
		if _, err := gitutil.GeneratePRTemplate(codeDir, r.cfg.UserPrompt, r.cfg.Writer.Name(), r.cfg.Auditor.Name()); err != nil {
			r.warn("pr template generation failed", err, map[string]any{"dir": codeDir})
		}
	}

	r.cfg.Log.Info("session complete")
	r.cfg.Events <- llm.Event{Kind: llm.EventDone}
}

func (r *Runner) passPrompt(idx int) string {
	if idx < len(r.cfg.PassPrompts) {
		return r.cfg.PassPrompts[idx]
	}
	return fmt.Sprintf("Pass: %s", r.cfg.Passes[idx].Name)
}

func (r *Runner) auditorPrompt(idx int) string {
	if idx < len(r.cfg.AuditorPrompts) {
		return r.cfg.AuditorPrompts[idx]
	}
	// fallback: use writer prompt (original behaviour)
	return r.passPrompt(idx)
}

func (r *Runner) warn(msg string, err error, fields map[string]any) {
	payload := map[string]any{}
	for k, v := range fields {
		payload[k] = v
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	r.cfg.Log.Warn(msg, payload)
	if err != nil {
		r.cfg.Events <- llm.Event{Kind: llm.EventWarning, Err: fmt.Errorf("%s: %w", msg, err)}
	}
}
