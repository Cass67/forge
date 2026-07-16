package session

import (
	"context"
	"fmt"
	"strings"

	"forge/internal/llm"
	"forge/internal/logger"
	"forge/internal/output"
	"forge/internal/summarizer"
)

// approvedSignal is what the auditor writes on its own line when satisfied.
const approvedSignal = "APPROVED"

// Round executes one writer → auditor → summarizer cycle.
type Round struct {
	writer          llm.Driver
	auditor         llm.Driver
	sumAgent        *summarizer.Agent
	writerW         *output.Writer
	store           *summarizer.Store
	events          chan<- llm.Event
	gate            *TurnGate
	log             *logger.Logger
	tracker         *llm.UsageTracker
	lastAuditorTurn string
}

func NewRound(
	writer, auditor llm.Driver,
	sumAgent *summarizer.Agent,
	w *output.Writer,
	store *summarizer.Store,
	events chan<- llm.Event,
	gate *TurnGate,
	log *logger.Logger,
	tracker *llm.UsageTracker,
) *Round {
	if log == nil {
		log = logger.Nop()
	}
	return &Round{
		writer:   writer,
		auditor:  auditor,
		sumAgent: sumAgent,
		writerW:  w,
		store:    store,
		events:   events,
		gate:     gate,
		log:      log,
		tracker:  tracker,
	}
}

// Run executes AI-1 → code write → AI-2 → summarize.
// Returns converged=true when the auditor signals APPROVED, meaning no more
// rounds are needed for this pass.
func (r *Round) Run(
	ctx context.Context,
	writerPrompt, auditorPrompt string,
	passIdx, roundIdx int,
	userPrompt, languageHint, summaryStoreText, priorAuditorTurn string,
) (converged bool, err error) {
	// 0. Snapshot code before this round (for diff log)
	beforeSnap := r.writerW.TakeSnapshot()

	// 1. Inline existing code files
	inlined, inlineErr := r.writerW.InlineCodeFiles()
	if inlineErr != nil {
		return false, fmt.Errorf("inline code: %w", inlineErr)
	}

	userContent := buildUserContent(userPrompt, languageHint, inlined, summaryStoreText, priorAuditorTurn, "AI-2 LAST TURN")

	// 2. Writer call
	r.log.Debug("writer call started", map[string]any{"pass": passIdx, "round": roundIdx})
	writerText, err := r.streamAgent(ctx, "writer", writerPrompt, userContent, passIdx, roundIdx)
	if err != nil {
		return false, err
	}
	r.log.Debug("writer call complete", map[string]any{"pass": passIdx, "round": roundIdx, "output_len": len(writerText)})
	if err := r.writerW.AppendAgentTranscript("writer", passIdx, roundIdx, writerText); err != nil {
		return false, fmt.Errorf("append AI-1 transcript: %w", err)
	}

	// 3. Parse and write AI-1 code blocks
	blocks := output.ParseCodeBlocks(writerText)
	if len(blocks) > 0 {
		r.log.Info("code written", map[string]any{"agent": "writer", "files": len(blocks)})
	}
	if err := r.applyCodeBlocks(writerText); err != nil {
		return false, err
	}
	r.events <- llm.Event{Kind: llm.EventAgentDone, Agent: "writer", Pass: passIdx, Round: roundIdx}
	if r.gate != nil {
		r.gate.Wait()
	}

	// 4. Re-inline updated code for auditor
	inlined, inlineErr = r.writerW.InlineCodeFiles()
	if inlineErr != nil {
		return false, fmt.Errorf("inline code for auditor: %w", inlineErr)
	}
	auditorUserContent := buildUserContent(userPrompt, languageHint, inlined, summaryStoreText, writerText, "AI-1 LAST TURN")

	// 5. Auditor call
	r.log.Debug("auditor call started", map[string]any{"pass": passIdx, "round": roundIdx})
	auditorText, err := r.streamAgent(ctx, "auditor", auditorPrompt, auditorUserContent, passIdx, roundIdx)
	if err != nil {
		return false, err
	}
	r.log.Debug("auditor call complete", map[string]any{"pass": passIdx, "round": roundIdx, "output_len": len(auditorText), "converged": auditorApproved(auditorText)})
	r.lastAuditorTurn = auditorText
	if err := r.writerW.AppendAgentTranscript("auditor", passIdx, roundIdx, auditorText); err != nil {
		return false, fmt.Errorf("append AI-2 transcript: %w", err)
	}

	// 6. Parse and write AI-2 code blocks as direct fixes/patches.
	if err := r.applyCodeBlocks(auditorText); err != nil {
		return false, err
	}
	r.events <- llm.Event{Kind: llm.EventAgentDone, Agent: "auditor", Pass: passIdx, Round: roundIdx}
	if r.gate != nil {
		r.gate.Wait()
	}

	// 7. Detect convergence — auditor writes APPROVED on its own line when done
	converged = auditorApproved(auditorText)

	// 8. Summarize
	summaryBody, sumErr := r.sumAgent.SummarizeRound(ctx, writerText, auditorText)
	if sumErr != nil {
		r.store.AppendPlaceholder(passIdx, roundIdx)
	} else {
		r.store.Append(summarizer.Entry{Pass: passIdx, Round: roundIdx, Body: summaryBody})
	}

	// 9. Diff log — capture what changed this round
	afterSnap := r.writerW.TakeSnapshot()
	diffs := output.DiffSnapshots(beforeSnap, afterSnap)
	r.writerW.AppendDiffLog(passIdx, roundIdx, diffs)

	r.events <- llm.Event{Kind: llm.EventRoundEnd, Pass: passIdx, Round: roundIdx}
	return converged, nil
}

func (r *Round) LastAuditorTurn() string {
	return r.lastAuditorTurn
}

func (r *Round) applyCodeBlocks(text string) error {
	blocks := output.ParseCodeBlocks(text)
	for _, b := range blocks {
		if err := r.writerW.WriteCode(b); err != nil {
			return fmt.Errorf("write code block %s: %w", b.Filename, err)
		}
	}
	return nil
}

// auditorApproved returns true if the auditor's response contains APPROVED
// on its own line, signalling it has no further issues with the code.
func auditorApproved(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == approvedSignal {
			return true
		}
	}
	return false
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
			text := tok.Text
			if tok.ReasoningContent != "" {
				text = tok.ReasoningContent
			}
			sb.WriteString(text)
			r.events <- llm.Event{Kind: llm.EventToken, Agent: agent, Text: text, Pass: pass, Round: round}
		}
	}
	if err := <-errCh; err != nil {
		return sb.String(), err
	}

	// Record token usage if driver supports it
	if r.tracker != nil {
		var usage llm.Usage
		if reporter, ok := driver.(llm.UsageReporter); ok {
			usage = reporter.LastUsage()
		} else {
			usage = llm.Usage{OutputTokens: llm.EstimateTokens(sb.String())}
		}
		r.tracker.Record(llm.UsageEntry{
			Agent: agent,
			Model: driver.Name(),
			Pass:  pass,
			Round: round,
			Usage: usage,
		})
	}

	return sb.String(), nil
}

func buildUserContent(prompt, languageHint, inlinedCode, summaryStore, priorTurn, roleLabel string) string {
	var sb strings.Builder
	sb.WriteString("USER GOAL:\n")
	sb.WriteString(prompt)
	if guidance := buildLanguageGuidance(languageHint, inlinedCode); guidance != "" {
		sb.WriteString("\n\nLANGUAGE GUIDANCE:\n")
		sb.WriteString(guidance)
	}
	if inlinedCode != "" {
		sb.WriteString("\n\nCURRENT CODE:\n")
		sb.WriteString(inlinedCode)
	}
	if summaryStore != "" {
		sb.WriteString("\n\nSESSION HISTORY:\n")
		sb.WriteString(summaryStore)
	}
	if priorTurn != "" {
		sb.WriteString("\n\nTURN PROTOCOL:\n")
		sb.WriteString("First respond to the other agent's comments, critiques, or patches in plain language.\n")
		sb.WriteString("After that, emit any revised code blocks needed to move the code forward.\n")
		sb.WriteString("\n\n")
		if roleLabel == "" {
			roleLabel = "OTHER AGENT LAST TURN"
		}
		sb.WriteString(roleLabel)
		sb.WriteString(":\n")
		sb.WriteString(priorTurn)
	}
	return sb.String()
}

func buildLanguageGuidance(languageHint, inlinedCode string) string {
	hint := strings.TrimSpace(languageHint)
	switch {
	case hint == "", strings.EqualFold(hint, "auto"):
		if strings.TrimSpace(inlinedCode) != "" {
			return "Match the language, framework, and project conventions already present in CURRENT CODE. Do not switch languages unless the user explicitly asks for a rewrite."
		}
		return "Choose the best language, framework, and file layout for the task. Do not default to Go unless the task clearly calls for it."
	default:
		return fmt.Sprintf("Use %s unless the existing codebase shown below makes that unsafe or incompatible.", hint)
	}
}
