package drivers

import (
	"context"
	"sync"

	"forge/internal/llm"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type ClaudeDriver struct {
	client    *anthropic.Client
	name      string
	model     string
	params    llm.Params
	lastUsage llm.Usage
	lastMode  string
	mu        sync.Mutex
}

func NewClaude(apiKey, model string) *ClaudeDriver {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &ClaudeDriver{client: &client, name: model, model: model, params: llmDefaultParams()}
}

func (d *ClaudeDriver) SetParams(p llm.Params) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.params = p
}

func (d *ClaudeDriver) Name() string { return d.name }

func (d *ClaudeDriver) LastUsage() llm.Usage {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastUsage
}

func (d *ClaudeDriver) LastRequestMode() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastMode
}

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

	maxTok := int64(8096)
	if d.params.MaxTokens > 0 {
		maxTok = int64(d.params.MaxTokens)
	}
	apiParams := anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: maxTok,
		Messages:  chatMsgs,
		CacheControl: anthropic.CacheControlEphemeralParam{
			TTL: anthropic.CacheControlEphemeralTTLTTL5m,
		},
	}

	if len(systemBlocks) > 0 {
		apiParams.System = systemBlocks
	}

	if d.model != "" {
		apiParams.Model = anthropic.Model(d.model)
	}

	if d.params.Temperature >= 0 {
		apiParams.Temperature = anthropic.Float(d.params.Temperature)
	}
	d.mu.Lock()
	d.lastMode = "claude prompt cache (ephemeral 5m)"
	d.mu.Unlock()

	var acc anthropic.Message
	stream := d.client.Messages.NewStreaming(ctx, apiParams)
	for stream.Next() {
		event := stream.Current()
		acc.Accumulate(event)
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
	if err := stream.Err(); err != nil {
		return err
	}

	d.mu.Lock()
	d.lastUsage = llm.Usage{
		InputTokens:  int(acc.Usage.InputTokens),
		OutputTokens: int(acc.Usage.OutputTokens),
	}
	d.mu.Unlock()

	return nil
}
