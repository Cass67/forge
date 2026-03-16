package session

import (
    "context"
    "fmt"
    "strings"

    "forge/internal/llm"
    "forge/internal/output"
    "forge/internal/summarizer"
)

// Round executes one writer → auditor → summarizer cycle.
type Round struct {
    writer   llm.Driver
    auditor  llm.Driver
    sumAgent *summarizer.Agent
    writerW  *output.Writer
    store    *summarizer.Store
    events   chan<- llm.Event
}

func NewRound(
    writer, auditor llm.Driver,
    sumAgent *summarizer.Agent,
    w *output.Writer,
    store *summarizer.Store,
    events chan<- llm.Event,
) *Round {
    return &Round{
        writer:   writer,
        auditor:  auditor,
        sumAgent: sumAgent,
        writerW:  w,
        store:    store,
        events:   events,
    }
}

// Run executes writer → code write → auditor → summarize.
func (r *Round) Run(
    ctx context.Context,
    writerPrompt, auditorPrompt string,
    passIdx, roundIdx int,
    userPrompt, summaryStoreText string,
) error {
    // 1. Inline existing code files
    inlined, err := r.writerW.InlineCodeFiles()
    if err != nil {
        return fmt.Errorf("inline code: %w", err)
    }

    userContent := buildUserContent(userPrompt, inlined, summaryStoreText)

    // 2. Writer call
    writerText, err := r.streamAgent(ctx, "writer", writerPrompt, userContent, passIdx, roundIdx)
    if err != nil {
        return err
    }

    // 3. Parse and write code blocks
    blocks := output.ParseCodeBlocks(writerText)
    for _, b := range blocks {
        if err := r.writerW.WriteCode(b); err != nil {
            return fmt.Errorf("write code block %s: %w", b.Filename, err)
        }
    }

    // 4. Re-inline updated code for auditor
    inlined, err = r.writerW.InlineCodeFiles()
    if err != nil {
        return fmt.Errorf("inline code for auditor: %w", err)
    }
    auditorUserContent := buildUserContent(userPrompt, inlined, summaryStoreText)

    // 5. Auditor call
    auditorText, err := r.streamAgent(ctx, "auditor", auditorPrompt, auditorUserContent, passIdx, roundIdx)
    if err != nil {
        return err
    }

    // 6. Summarize
    summaryBody, sumErr := r.sumAgent.SummarizeRound(ctx, writerText, auditorText)
    if sumErr != nil {
        r.store.AppendPlaceholder(passIdx, roundIdx)
    } else {
        r.store.Append(summarizer.Entry{Pass: passIdx, Round: roundIdx, Body: summaryBody})
    }

    r.events <- llm.Event{Kind: llm.EventRoundEnd, Pass: passIdx, Round: roundIdx}
    return nil
}

func (r *Round) streamAgent(ctx context.Context, agent, systemPrompt, userContent string, pass, round int) (string, error) {
    messages := []llm.Message{
        {Role: llm.RoleSystem, Content: systemPrompt},
        {Role: llm.RoleUser, Content: userContent},
    }
    out := make(chan llm.Token, 64)
    var sb strings.Builder
    errCh := make(chan error, 1)

    driver := r.writer
    if agent == "auditor" {
        driver = r.auditor
    }

    go func() {
        errCh <- driver.Stream(ctx, messages, out)
    }()

    for tok := range out {
        if tok.Err != nil {
            <-errCh
            return sb.String(), tok.Err
        }
        if !tok.Done {
            sb.WriteString(tok.Text)
            r.events <- llm.Event{Kind: llm.EventToken, Agent: agent, Text: tok.Text, Pass: pass, Round: round}
        }
    }
    return sb.String(), <-errCh
}

func buildUserContent(prompt, inlinedCode, summaryStore string) string {
    var sb strings.Builder
    sb.WriteString("USER GOAL:\n")
    sb.WriteString(prompt)
    if inlinedCode != "" {
        sb.WriteString("\n\nCURRENT CODE:\n")
        sb.WriteString(inlinedCode)
    }
    if summaryStore != "" {
        sb.WriteString("\n\nSESSION HISTORY:\n")
        sb.WriteString(summaryStore)
    }
    return sb.String()
}
