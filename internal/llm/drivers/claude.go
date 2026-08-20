package drivers

import (
	"context"
	"strings"
	"sync"

	"forge/internal/llm"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type ClaudeDriver struct {
	client       *anthropic.Client
	name         string
	model        string
	promptCache  bool
	systemPrefix string
	params       llm.Params
	lastUsage    llm.Usage
	lastMode     string
	mu           sync.Mutex
}

func NewClaude(apiKey, model string) *ClaudeDriver {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &ClaudeDriver{client: &client, name: model, model: model, promptCache: true, params: llmDefaultParams()}
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
	apiParams := buildClaudeBetaParams(d.model, d.params, messages, maxTok, claudeRequestOptions{
		promptCache:  d.promptCache,
		systemPrefix: d.systemPrefix,
	})
	d.mu.Lock()
	if d.promptCache {
		d.lastMode = "claude prompt cache (ephemeral 5m)"
	} else {
		d.lastMode = "claude oauth"
	}
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
	// Anthropic reports input_tokens net of cache; fold the cache counts back
	// in so InputTokens means the same thing as it does for OpenAI.
	cacheRead := int(acc.Usage.CacheReadInputTokens)
	cacheWrite := int(acc.Usage.CacheCreationInputTokens)
	d.lastUsage = llm.Usage{
		InputTokens:       int(acc.Usage.InputTokens) + cacheRead + cacheWrite,
		OutputTokens:      int(acc.Usage.OutputTokens),
		CachedInputTokens: cacheRead,
		CacheWriteTokens:  cacheWrite,
	}
	d.mu.Unlock()

	return nil
}

type claudeRequestOptions struct {
	promptCache  bool
	systemPrefix string
}

func buildClaudeBetaMessages(messages []llm.Message, systemPrefix string) ([]anthropic.BetaTextBlockParam, []anthropic.BetaMessageParam) {
	var systemBlocks []anthropic.BetaTextBlockParam
	var chatMsgs []anthropic.BetaMessageParam
	if strings.TrimSpace(systemPrefix) != "" {
		systemBlocks = append(systemBlocks, anthropic.BetaTextBlockParam{
			Text: strings.TrimSpace(systemPrefix),
		})
	}
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

func buildClaudeBetaParams(model string, params llm.Params, messages []llm.Message, maxTokens int64, opts claudeRequestOptions) anthropic.BetaMessageNewParams {
	systemBlocks, chatMsgs := buildClaudeBetaMessages(messages, opts.systemPrefix)
	apiParams := anthropic.BetaMessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: maxTokens,
		Messages:  chatMsgs,
	}
	if opts.promptCache {
		apiParams.CacheControl = anthropic.BetaCacheControlEphemeralParam{
			TTL: anthropic.BetaCacheControlEphemeralTTLTTL5m,
		}
		apiParams.Betas = []anthropic.AnthropicBeta{
			anthropic.AnthropicBetaPromptCaching2024_07_31,
		}
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
