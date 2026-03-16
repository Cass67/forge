package session

import (
    "context"
    "fmt"
    "os"
    "path/filepath"

    "forge/internal/llm"
    "forge/internal/output"
    "forge/internal/summarizer"
)

// RunnerConfig holds everything the runner needs to execute a session.
type RunnerConfig struct {
    Passes       []Pass
    Writer       llm.Driver
    Auditor      llm.Driver
    SumDriver    llm.Driver
    SumPrompt    string // per-round summarizer system prompt
    AuditPrompt  string // audit-log system prompt (used at end)
    WriterW      *output.Writer
    Store        *summarizer.Store
    Events       chan<- llm.Event
    UserPrompt   string
    ContextFiles []string // paths of context files inlined in round 1
    PassPrompts  []string // system prompts per pass (indexed by pass order)
}

// Runner orchestrates the full session: all passes and rounds.
type Runner struct {
    cfg RunnerConfig
}

func NewRunner(cfg RunnerConfig) *Runner {
    return &Runner{cfg: cfg}
}

// Run executes all passes. It sends events to cfg.Events and closes the channel when done.
func (r *Runner) Run(ctx context.Context) {
    defer close(r.cfg.Events)

    sumAgent := summarizer.NewAgent(r.cfg.SumDriver, r.cfg.SumPrompt)

    for passIdx, pass := range r.cfg.Passes {
        passNum := passIdx + 1

        writerPrompt := r.passPrompt(passIdx)
        auditorPrompt := writerPrompt

        for roundNum := 1; roundNum <= pass.Rounds; roundNum++ {
            if ctx.Err() != nil {
                r.cfg.Events <- llm.Event{Kind: llm.EventAbort, Err: ctx.Err()}
                return
            }

            storeText, _ := r.cfg.Store.ReadAll()

            round := NewRound(
                r.cfg.Writer,
                r.cfg.Auditor,
                sumAgent,
                r.cfg.WriterW,
                r.cfg.Store,
                r.cfg.Events,
            )

            if err := round.Run(ctx, writerPrompt, auditorPrompt, passNum, roundNum,
                r.cfg.UserPrompt, storeText); err != nil {
                r.cfg.Events <- llm.Event{Kind: llm.EventError, Pass: passNum, Round: roundNum, Err: err}
                return
            }
        }

        r.cfg.Events <- llm.Event{Kind: llm.EventPassEnd, Pass: passNum}
    }

    // generate audit log
    storeText, _ := r.cfg.Store.ReadAll()
    auditAgent := summarizer.NewAgent(r.cfg.SumDriver, r.cfg.AuditPrompt)
    auditText, err := auditAgent.GenerateAuditLog(ctx, "", storeText)
    if err == nil {
        os.WriteFile(filepath.Join(r.cfg.WriterW.Dir(), "audit-log.md"), []byte(auditText), 0o644)
    }

    r.cfg.Events <- llm.Event{Kind: llm.EventDone}
}

func (r *Runner) passPrompt(idx int) string {
    if idx < len(r.cfg.PassPrompts) {
        return r.cfg.PassPrompts[idx]
    }
    return fmt.Sprintf("Pass: %s", r.cfg.Passes[idx].Name)
}
