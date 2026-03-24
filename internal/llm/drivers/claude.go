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

	maxTok := int64(8096)
	if d.params.MaxTokens > 0 {
		maxTok = int64(d.params.MaxTokens)
	}
	apiParams := buildClaudeBetaParams(d.model, d.params, messages, maxTok)
	d.mu.Lock()
	d.lastMode = "claude prompt cache (ephemeral 5m)"
	d.mu.Unlock()

	var acc anthropic.BetaMessage
	stream := d.client.Beta.Messages.NewStreaming(ctx, apiParams)
	for stream.Next() {
		event := stream.Current()
		if err := acc.Accumulate(event); err != nil {
			return err
		}
		switch e := event.AsAny().(type) {
		case anthropic.BetaRawContentBlockDeltaEvent:
			if delta, ok := e.Delta.AsAny().(anthropic.BetaTextDelta); ok {
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

func buildClaudeBetaMessages(messages []llm.Message) ([]anthropic.BetaTextBlockParam, []anthropic.BetaMessageParam) {
	var systemBlocks []anthropic.BetaTextBlockParam
	var chatMsgs []anthropic.BetaMessageParam
	for _, m := range messages {
		switch m.Role {
		case llm.RoleSystem:
			systemBlocks = append(systemBlocks, anthropic.BetaTextBlockParam{
				Text: m.Content,
			})
		case llm.RoleUser:
			chatMsgs = append(chatMsgs, anthropic.NewBetaUserMessage(
				anthropic.BetaContentBlockParamUnion{
					OfText: &anthropic.BetaTextBlockParam{
						Text: m.Content,
					},
				},
			))
		case llm.RoleAssistant:
			chatMsgs = append(chatMsgs, anthropic.BetaMessageParam{
				Role: anthropic.BetaMessageParamRoleAssistant,
				Content: []anthropic.BetaContentBlockParamUnion{
					{
						OfText: &anthropic.BetaTextBlockParam{
							Text: m.Content,
						},
					},
				},
			})
		}
	}
	return systemBlocks, chatMsgs
}

func buildClaudeBetaParams(model string, params llm.Params, messages []llm.Message, maxTokens int64) anthropic.BetaMessageNewParams {
	systemBlocks, chatMsgs := buildClaudeBetaMessages(messages)
	apiParams := anthropic.BetaMessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: maxTokens,
		Messages:  chatMsgs,
		CacheControl: anthropic.BetaCacheControlEphemeralParam{
			TTL: anthropic.BetaCacheControlEphemeralTTLTTL5m,
		},
		Betas: []anthropic.AnthropicBeta{
			anthropic.AnthropicBetaPromptCaching2024_07_31,
		},
	}
	if len(systemBlocks) > 0 {
		apiParams.System = systemBlocks
	}
	if model != "" {
		apiParams.Model = anthropic.Model(model)
	}
	if params.Temperature >= 0 {
		apiParams.Temperature = anthropic.Float(params.Temperature)
	}
	return apiParams
}
