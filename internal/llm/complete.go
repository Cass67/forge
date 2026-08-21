package llm

import (
	"context"
	"strings"
)

// Complete runs a driver to the end of its stream and returns the joined text.
// It exists for the one-shot side calls — commit messages, classifiers, review
// summaries — that want an answer rather than a live token feed.
func Complete(ctx context.Context, d Driver, messages []Message) (string, error) {
	out := make(chan Token, 32)
	errCh := make(chan error, 1)
	go func() { errCh <- d.Stream(ctx, messages, out) }()

	var text strings.Builder
	drain := func() {
		for {
			select {
			case tok, ok := <-out:
				if !ok {
					return
				}
				text.WriteString(tok.Text)
			default:
				return
			}
		}
	}
	for {
		select {
		case tok, ok := <-out:
			if !ok {
				// The stream closed before Stream returned; wait for its error.
				select {
				case err := <-errCh:
					return text.String(), err
				case <-ctx.Done():
					return text.String(), ctx.Err()
				}
			}
			text.WriteString(tok.Text)
		case err := <-errCh:
			drain()
			return text.String(), err
		case <-ctx.Done():
			return text.String(), ctx.Err()
		}
	}
}
