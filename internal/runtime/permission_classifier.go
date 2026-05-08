package runtime

import (
	"context"
	"strings"
	"time"

	"forge/internal/llm"
	"forge/internal/permissions"
)

type llmPermissionClassifier struct {
	driver  llm.Driver
	timeout time.Duration
}

func newLLMPermissionClassifier(driver llm.Driver, timeout time.Duration) permissions.Classifier {
	if driver == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &llmPermissionClassifier{driver: driver, timeout: timeout}
}

func (c *llmPermissionClassifier) Classify(ctx context.Context, req permissions.ClassifierRequest) (permissions.ClassifierResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	out := make(chan llm.Token, 16)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.driver.Stream(ctx, []llm.Message{{Role: llm.RoleUser, Content: permissions.BuildClassifierPrompt(req)}}, out)
	}()
	var text strings.Builder
	for {
		select {
		case tok, ok := <-out:
			if !ok {
				out = nil
				continue
			}
			text.WriteString(tok.Text)
		case err := <-errCh:
			if err != nil {
				return permissions.ClassifierResponse{Decision: permissions.ClassifierAsk}, err
			}
			for {
				select {
				case tok, ok := <-out:
					if !ok {
						return permissions.ParseClassifierResponse(text.String())
					}
					text.WriteString(tok.Text)
				default:
					return permissions.ParseClassifierResponse(text.String())
				}
			}
		case <-ctx.Done():
			return permissions.ClassifierResponse{Decision: permissions.ClassifierAsk}, ctx.Err()
		}
	}
}
