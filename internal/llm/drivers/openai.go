package drivers

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"forge/internal/llm"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

type OpenAIDriver struct {
	client            *openai.Client
	registryName      string // Name() — used for registry lookup; may include provider prefix
	apiModel          string // model ID sent to the API
	supportsResponses bool
	params            llm.Params
	lastUsage         llm.Usage
	mu                sync.Mutex
}

func NewOpenAI(apiKey, model string) *OpenAIDriver {
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAIDriver{
		client:            &client,
		registryName:      model,
		apiModel:          model,
		supportsResponses: true,
		params:            llm.Params{Temperature: -1},
	}
}

func NewOpenAICompatible(apiKey, baseURL, model string) *OpenAIDriver {
	client := openai.NewClient(option.WithAPIKey(apiKey), option.WithBaseURL(baseURL))
	return &OpenAIDriver{
		client:       &client,
		registryName: model,
		apiModel:     model,
		params:       llm.Params{Temperature: -1},
	}
}

// NewCopilot creates a driver for GitHub Copilot. registryName is the key used
// in the registry (e.g. "copilot/gpt-4o"); apiModel is the bare model ID sent
// to the Copilot API (e.g. "gpt-4o").
func NewCopilot(token, registryName, apiModel string) *OpenAIDriver {
	client := openai.NewClient(
		option.WithAPIKey(token),
		option.WithBaseURL("https://api.githubcopilot.com"),
		option.WithHeader("Copilot-Integration-Id", "copilot-developer-cli"),
		option.WithHeader("Openai-Intent", "conversation-agent"),
		option.WithHeader("X-Initiator", "user"),
		option.WithHeader("X-GitHub-Api-Version", "2025-05-01"),
	)
	return &OpenAIDriver{
		client:            &client,
		registryName:      registryName,
		apiModel:          apiModel,
		supportsResponses: true,
		params:            llm.Params{Temperature: -1},
	}
}

func (d *OpenAIDriver) SetParams(p llm.Params) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.params = p
}

func (d *OpenAIDriver) Name() string { return d.registryName }

func (d *OpenAIDriver) LastUsage() llm.Usage {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastUsage
}

func (d *OpenAIDriver) Stream(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	defer close(out)

	if d.useResponsesAPI() {
		return d.streamResponses(ctx, messages, out)
	}
	return d.streamChatCompletions(ctx, messages, out)
}

func (d *OpenAIDriver) streamChatCompletions(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(d.apiModel),
		Messages: toOpenAIMessages(messages),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}
	if d.params.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(d.params.MaxTokens))
	}
	if d.params.Temperature >= 0 {
		params.Temperature = openai.Float(d.params.Temperature)
	}

	var outputChars int
	stream := d.client.Chat.Completions.NewStreaming(ctx, params)
	for stream.Next() {
		chunk := stream.Current()
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			d.mu.Lock()
			d.lastUsage = llm.Usage{
				InputTokens:  int(chunk.Usage.PromptTokens),
				OutputTokens: int(chunk.Usage.CompletionTokens),
			}
			d.mu.Unlock()
		}
		for _, choice := range chunk.Choices {
			text := choice.Delta.Content
			if text == "" {
				continue
			}
			outputChars += len(text)
			select {
			case out <- llm.Token{Text: text}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("openai stream (model: %s): %w", d.apiModel, err)
	}

	d.mu.Lock()
	if d.lastUsage.OutputTokens == 0 && outputChars > 0 {
		d.lastUsage.OutputTokens = (outputChars + 3) / 4
	}
	d.mu.Unlock()

	return nil
}

func (d *OpenAIDriver) streamResponses(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	params := responses.ResponseNewParams{
		Model: d.apiModel,
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: toResponseInput(messages),
		},
	}
	if d.params.MaxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(d.params.MaxTokens))
	}
	if d.params.Temperature >= 0 {
		params.Temperature = openai.Float(d.params.Temperature)
	}

	stream := d.client.Responses.NewStreaming(ctx, params)
	for stream.Next() {
		switch event := stream.Current().AsAny().(type) {
		case responses.ResponseTextDeltaEvent:
			if event.Delta == "" {
				continue
			}
			select {
			case out <- llm.Token{Text: event.Delta}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("openai stream (model: %s): %w", d.apiModel, err)
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

func toResponseInput(msgs []llm.Message) []responses.ResponseInputItemUnionParam {
	out := make([]responses.ResponseInputItemUnionParam, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			out = append(out, responses.ResponseInputItemParamOfMessage(m.Content, responses.EasyInputMessageRoleSystem))
		case llm.RoleUser:
			out = append(out, responses.ResponseInputItemParamOfMessage(m.Content, responses.EasyInputMessageRoleUser))
		case llm.RoleAssistant:
			out = append(out, responses.ResponseInputItemParamOfMessage(m.Content, responses.EasyInputMessageRoleAssistant))
		}
	}
	return out
}

func (d *OpenAIDriver) useResponsesAPI() bool {
	return d.supportsResponses && modelRequiresResponses(d.apiModel)
}

func modelRequiresResponses(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gpt-5") || strings.HasPrefix(m, "gpt5")
}
