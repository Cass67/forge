package drivers

import (
	"context"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
	"forge/internal/llm"
)

type OpenAIDriver struct {
	client *openai.Client
	model  string
}

func NewOpenAI(apiKey, model string) *OpenAIDriver {
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAIDriver{client: &client, model: model}
}

func (d *OpenAIDriver) Name() string { return d.model }

func (d *OpenAIDriver) Stream(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	defer close(out)

	params := openai.ChatCompletionNewParams{
		Model:     shared.ChatModel(d.model),
		Messages:  toOpenAIMessages(messages),
	}

	stream := d.client.Chat.Completions.NewStreaming(ctx, params)
	for stream.Next() {
		chunk := stream.Current()
		for _, choice := range chunk.Choices {
			text := choice.Delta.Content
			if text == "" {
				continue
			}
			select {
			case out <- llm.Token{Text: text}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("openai stream: %w", err)
	}
	return nil
}

func toOpenAIMessages(msgs []llm.Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			out = append(out, openai.SystemMessage(m.Content))
		case llm.RoleUser:
			out = append(out, openai.UserMessage(m.Content))
		case llm.RoleAssistant:
			out = append(out, openai.AssistantMessage(m.Content))
		}
	}
	return out
}
