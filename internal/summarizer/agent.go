package summarizer

import (
	"context"
	"fmt"
	"strings"

	"forge/internal/llm"
)

// Agent calls an LLM to produce summary entries and audit logs.
type Agent struct {
	driver       llm.Driver
	systemPrompt string
}

func NewAgent(driver llm.Driver, systemPrompt string) *Agent {
	return &Agent{driver: driver, systemPrompt: systemPrompt}
}

// SummarizeRound calls the LLM to summarize one writer+auditor exchange.
func (a *Agent) SummarizeRound(ctx context.Context, writerText, auditorText string) (string, error) {
	userContent := fmt.Sprintf("WRITER OUTPUT:\n%s\n\nAUDITOR CRITIQUE:\n%s", writerText, auditorText)
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: a.systemPrompt},
		{Role: llm.RoleUser, Content: userContent},
	}
	return a.collect(ctx, messages)
}

// GenerateAuditLog calls the LLM to produce the final audit-log.md from the full summary store.
func (a *Agent) GenerateAuditLog(ctx context.Context, auditSystemPrompt, storeContents string) (string, error) {
	sysPrompt := a.systemPrompt
	if auditSystemPrompt != "" {
		sysPrompt = auditSystemPrompt
	}
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: sysPrompt},
		{Role: llm.RoleUser, Content: "FULL SESSION LOG:\n" + storeContents},
	}
	return a.collect(ctx, messages)
}

// collect buffers the full stream response into a string.
func (a *Agent) collect(ctx context.Context, messages []llm.Message) (string, error) {
	out := make(chan llm.Token, 64)
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.driver.Stream(ctx, messages, out)
	}()

	var sb strings.Builder
	for tok := range out {
		if tok.Err != nil {
			return "", tok.Err
		}
		if !tok.Done {
			sb.WriteString(tok.Text)
		}
	}
	if err := <-errCh; err != nil {
		return sb.String(), err
	}
	return sb.String(), nil
}
