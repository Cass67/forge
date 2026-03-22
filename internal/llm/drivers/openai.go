package drivers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"forge/internal/copilot"
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
	debugRequestSizing(d.registryName, d.apiModel, messages)

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
			usage := llm.Usage{
				InputTokens:  int(chunk.Usage.PromptTokens),
				OutputTokens: int(chunk.Usage.CompletionTokens),
			}
			if quota := copilot.ExtractQuotaJSON(chunk.RawJSON()); quota != nil {
				usage.CopilotQuota = quota
				debugCopilotQuota("chat.chunk", d.apiModel, quota, chunk.RawJSON())
			}
			d.mu.Lock()
			d.lastUsage = usage
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
		return d.wrapStreamError("chat.completions", err)
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
		evt := stream.Current()
		if quota := copilot.ExtractQuotaJSON(evt.RawJSON()); quota != nil {
			debugCopilotQuota("responses.event", d.apiModel, quota, evt.RawJSON())
			d.mu.Lock()
			usage := d.lastUsage
			usage.CopilotQuota = quota
			d.lastUsage = usage
			d.mu.Unlock()
		}
		switch event := evt.AsAny().(type) {
		case responses.ResponseTextDeltaEvent:
			if event.Delta == "" {
				continue
			}
			select {
			case out <- llm.Token{Text: event.Delta}:
			case <-ctx.Done():
				return ctx.Err()
			}
		case responses.ResponseCompletedEvent:
			usage := llm.Usage{}
			if event.Response.Usage.InputTokens > 0 || event.Response.Usage.OutputTokens > 0 {
				usage.InputTokens = int(event.Response.Usage.InputTokens)
				usage.OutputTokens = int(event.Response.Usage.OutputTokens)
			}
			if quota := copilot.ExtractQuotaJSON(event.Response.RawJSON()); quota != nil {
				usage.CopilotQuota = quota
				debugCopilotQuota("responses.completed", d.apiModel, quota, event.Response.RawJSON())
			}
			if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.CopilotQuota != nil {
				d.mu.Lock()
				if usage.InputTokens == 0 && usage.OutputTokens == 0 {
					usage.InputTokens = d.lastUsage.InputTokens
					usage.OutputTokens = d.lastUsage.OutputTokens
				}
				d.lastUsage = usage
				d.mu.Unlock()
			}
		}
	}
	if err := stream.Err(); err != nil {
		return d.wrapStreamError("responses", err)
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

func debugCopilotQuota(source, model string, quota *llm.CopilotQuota, raw string) {
	if strings.TrimSpace(os.Getenv("FORGE_DEBUG_COPILOT_QUOTA")) == "" || quota == nil {
		return
	}
	raw = strings.TrimSpace(raw)
	if len(raw) > 400 {
		raw = raw[:400] + "..."
	}
	fmt.Fprintf(os.Stderr, "[forge] copilot quota captured source=%s model=%s type=%s included=%d used=%d remaining=%d percent=%.2f reset=%s raw=%s\n",
		source, model, quota.Type, quota.Included, quota.Used, quota.Remaining, quota.PercentRemaining, quota.ResetAt, raw)
}

func debugRequestSizing(registryName, apiModel string, messages []llm.Message) {
	if strings.TrimSpace(os.Getenv("FORGE_DEBUG_COPILOT_QUOTA")) == "" {
		return
	}
	var totalBytes int
	type msgInfo struct {
		idx   int
		role  llm.Role
		bytes int
	}
	infos := make([]msgInfo, 0, len(messages))
	for i, m := range messages {
		sz := len([]byte(m.Content))
		totalBytes += sz
		infos = append(infos, msgInfo{idx: i, role: m.Role, bytes: sz})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].bytes > infos[j].bytes })
	parts := make([]string, 0, len(infos))
	for _, info := range infos {
		parts = append(parts, fmt.Sprintf("%d:%s=%dB", info.idx, info.role, info.bytes))
	}
	fmt.Fprintf(os.Stderr, "[forge] request sizing registry_model=%s api_model=%s messages=%d total_bytes=%d breakdown=%s\n",
		registryName, apiModel, len(messages), totalBytes, strings.Join(parts, ", "))
}

func (d *OpenAIDriver) wrapStreamError(api string, err error) error {
	msg := fmt.Sprintf("openai stream (api: %s, model: %s): %v", api, d.apiModel, err)
	detail := extractHTTPErrorDetails(err)
	if detail == "" {
		return fmt.Errorf("%s", msg)
	}
	return fmt.Errorf("%s [%s]", msg, detail)
}

func extractHTTPErrorDetails(err error) string {
	if err == nil {
		return ""
	}
	type headerCarrier interface{ Headers() http.Header }
	type bodyCarrier interface{ DumpRequest(bool) ([]byte, error) }
	_ = bodyCarrier(nil)
	parts := make([]string, 0, 6)
	errText := strings.TrimSpace(err.Error())
	if errText != "" {
		parts = append(parts, "err="+truncateDebug(errText, 280))
	}
	if h, ok := err.(headerCarrier); ok {
		headers := h.Headers()
		if reqID := strings.TrimSpace(headers.Get("X-GitHub-Request-Id")); reqID != "" {
			parts = append(parts, "request_id="+reqID)
		}
		if quota := copilot.ExtractQuotaHeaders(headers); quota != nil {
			debugCopilotQuota("error.headers", "", quota, headersSummary(headers))
			parts = append(parts, fmt.Sprintf("quota=%s remaining=%d included=%d used=%d percent=%.2f reset=%s", quota.Type, quota.Remaining, quota.Included, quota.Used, quota.PercentRemaining, quota.ResetAt))
		}
	}
	if quota := copilot.ExtractQuotaJSON(errText); quota != nil {
		debugCopilotQuota("error.body", "", quota, errText)
		parts = append(parts, fmt.Sprintf("quota_body=%s remaining=%d included=%d used=%d percent=%.2f reset=%s", quota.Type, quota.Remaining, quota.Included, quota.Used, quota.PercentRemaining, quota.ResetAt))
	}
	return strings.Join(parts, "; ")
}

func headersSummary(h http.Header) string {
	if len(h) == 0 {
		return ""
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := strings.TrimSpace(h.Get(k))
		if v == "" {
			continue
		}
		parts = append(parts, k+"="+truncateDebug(v, 120))
	}
	return truncateDebug(strings.Join(parts, ", "), 400)
}

func truncateDebug(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func (d *OpenAIDriver) useResponsesAPI() bool {
	return d.supportsResponses && modelRequiresResponses(d.apiModel)
}

func modelRequiresResponses(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gpt-5") || strings.HasPrefix(m, "gpt5")
}
