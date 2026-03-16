package drivers

import (
	"context"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"forge/internal/llm"
)

type ClaudeDriver struct {
	client *anthropic.Client
	model  string
}

func NewClaude(apiKey, model string) *ClaudeDriver {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &ClaudeDriver{client: &client, model: model}
}

func (d *ClaudeDriver) Name() string { return d.model }

func (d *ClaudeDriver) Stream(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	defer close(out)

	var systemBlocks []anthropic.TextBlockParam
	var chatMsgs []anthropic.MessageParam
	for _, m := range messages {
		switch m.Role {
		case llm.RoleSystem:
			systemBlocks = append(systemBlocks, anthropic.TextBlockParam{
				Text: m.Content,
			})
		case llm.RoleUser:
			chatMsgs = append(chatMsgs, anthropic.NewUserMessage(
				anthropic.ContentBlockParamUnion{
					OfText: &anthropic.TextBlockParam{
						Text: m.Content,
					},
				},
			))
		case llm.RoleAssistant:
			chatMsgs = append(chatMsgs, anthropic.NewAssistantMessage(
				anthropic.ContentBlockParamUnion{
					OfText: &anthropic.TextBlockParam{
						Text: m.Content,
					},
				},
			))
		}
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: 8096,
		Messages:  chatMsgs,
	}

	if len(systemBlocks) > 0 {
		params.System = systemBlocks
	}

	// Override model if provided
	if d.model != "" {
		params.Model = anthropic.Model(d.model)
	}

	stream := d.client.Messages.NewStreaming(ctx, params)
	for stream.Next() {
		event := stream.Current()
		switch e := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			if delta, ok := e.Delta.AsAny().(anthropic.TextDelta); ok {
				select {
				case out <- llm.Token{Text: delta.Text}:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
	return stream.Err()
}
