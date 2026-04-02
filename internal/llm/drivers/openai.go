package drivers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"forge/internal/chatgptauth"
	"forge/internal/copilot"
	"forge/internal/llm"
	"forge/internal/modelcatalog"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

type OpenAIDriver struct {
	client            *openai.Client
	providerLabel     string
	registryName      string // Name() — used for registry lookup; may include provider prefix
	apiModel          string // model ID sent to the API
	supportsResponses bool
	forceResponses    bool
	params            llm.Params
	lastUsage         llm.Usage
	prevResponseID    string
	lastMessages      []llm.Message
	lastRequestMode   string
	mu                sync.Mutex
}

const (
	responseStateCompactionThreshold = 8000
	responseStatePreserveMessages    = 6
	openRouterReferer                = "https://github.com/cass/forge"
	openRouterTitle                  = "forge"
)

func NewOpenAI(apiKey, model string) *OpenAIDriver {
	return newOpenAI(strings.TrimSpace(apiKey), "openai", model, model, true, "", nil)
}

func newOpenAI(apiKey, providerLabel, registryName, apiModel string, supportsResponses bool, baseURL string, httpClient *http.Client) *OpenAIDriver {
	opts := []option.RequestOption{option.WithAPIKey(strings.TrimSpace(apiKey))}
	if strings.TrimSpace(baseURL) != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if httpClient != nil {
		opts = append(opts, option.WithHTTPClient(httpClient))
	}
	opts = append(opts, providerHeaders(providerLabel)...)
	client := openai.NewClient(opts...)
	return &OpenAIDriver{
		client:            &client,
		providerLabel:     providerLabel,
		registryName:      registryName,
		apiModel:          apiModel,
		supportsResponses: supportsResponses,
		params:            llm.Params{Temperature: -1},
	}
}

func NewOpenAIAlias(apiKey, registryName, apiModel string) *OpenAIDriver {
	return newOpenAI(apiKey, "openai", registryName, apiModel, true, "", nil)
}

func NewOpenAICompatibleAlias(apiKey, baseURL, registryName, apiModel string) *OpenAIDriver {
	return newOpenAI(apiKey, "openai", registryName, apiModel, false, baseURL, nil)
}

func NewOpenAICompatibleProviderAlias(providerLabel, apiKey, baseURL, registryName, apiModel string) *OpenAIDriver {
	return newOpenAI(apiKey, providerLabel, registryName, apiModel, false, baseURL, nil)
}

func NewOpenAICompatible(apiKey, baseURL, model string) *OpenAIDriver {
	return NewOpenAICompatibleAlias(apiKey, baseURL, model, model)
}

func NewCustomCompatProvider(providerLabel, apiKey, baseURL, registryName, apiModel string, supportsResponses bool, headers map[string]string) *OpenAIDriver {
	opts := []option.RequestOption{option.WithAPIKey(strings.TrimSpace(apiKey))}
	if strings.TrimSpace(baseURL) != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	for k, v := range headers {
		opts = append(opts, option.WithHeader(k, v))
	}
	opts = append(opts, providerHeaders(providerLabel)...)
	client := openai.NewClient(opts...)
	return &OpenAIDriver{
		client:            &client,
		providerLabel:     providerLabel,
		registryName:      registryName,
		apiModel:          apiModel,
		supportsResponses: supportsResponses,
		forceResponses:    supportsResponses,
		params:            llm.Params{Temperature: -1},
	}
}

// NewCopilot creates a driver for GitHub Copilot. registryName is the key used
// in the registry (e.g. "copilot/gpt-4o"); apiModel is the bare model ID sent
// to the Copilot API (e.g. "gpt-4o").
func NewCopilot(token, registryName, apiModel string) *OpenAIDriver {
	client := openai.NewClient(
		option.WithAPIKey(strings.TrimSpace(token)),
		option.WithBaseURL("https://api.githubcopilot.com"),
		option.WithHeader("Copilot-Integration-Id", "copilot-developer-cli"),
		option.WithHeader("Openai-Intent", "conversation-agent"),
		option.WithHeader("X-Initiator", "user"),
		option.WithHeader("X-GitHub-Api-Version", "2025-05-01"),
	)
	return &OpenAIDriver{
		client:            &client,
		providerLabel:     "copilot",
		registryName:      registryName,
		apiModel:          apiModel,
		supportsResponses: false, // Copilot only supports chat completions, not the Responses API
		params:            llm.Params{Temperature: -1},
	}
}

func NewChatGPT(registryName, apiModel string) *OpenAIDriver {
	authMgr, err := chatgptauth.NewManager()
	if err != nil {
		return nil
	}
	return newOpenAI("chatgpt-oauth", "chatgpt", registryName, apiModel, true, authMgr.BaseURL(), authMgr.HTTPClient())
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

func (d *OpenAIDriver) LastRequestMode() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastRequestMode
}

func (d *OpenAIDriver) ResetConversation() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.prevResponseID = ""
	d.lastMessages = nil
	d.lastRequestMode = ""
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
	params := d.chatCompletionParams(messages)

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
		if d.shouldFallbackToNonStreaming(err) {
			return d.chatCompletionsFallback(ctx, messages, out)
		}
		return d.wrapStreamError("chat.completions", err)
	}
	if outputChars == 0 && d.shouldFallbackAfterEmptyStream() {
		return d.chatCompletionsFallback(ctx, messages, out)
	}

	d.mu.Lock()
	if d.lastUsage.OutputTokens == 0 && outputChars > 0 {
		d.lastUsage.OutputTokens = (outputChars + 3) / 4
	}
	d.mu.Unlock()

	return nil
}

func (d *OpenAIDriver) chatCompletionParams(messages []llm.Message) openai.ChatCompletionNewParams {
	return d.chatCompletionParamsWithTools(messages, llm.NativeToolOptions{})
}

func (d *OpenAIDriver) chatCompletionParamsWithTools(messages []llm.Message, opts llm.NativeToolOptions) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(d.apiModel),
		Messages: toOpenAIMessages(messages),
	}
	if providerSupportsStreamUsageOptions(d.providerLabel) {
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}
	}
	if d.params.MaxTokens > 0 {
		if providerUsesLegacyMaxTokensField(d.providerLabel) {
			params.MaxTokens = openai.Int(int64(d.params.MaxTokens))
		} else {
			params.MaxCompletionTokens = openai.Int(int64(d.params.MaxTokens))
		}
	}
	if d.params.Temperature >= 0 && d.modelSupportsTemperature() {
		params.Temperature = openai.Float(d.params.Temperature)
	}
	if d.providerLabel == "openrouter" {
		params.PromptCacheKey = openai.String(responsePromptCacheKey(d.apiModel, chatPromptCacheSeed(messages)))
	}
	if opts.RequireToolCall {
		params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openai.Opt(string(openai.ChatCompletionToolChoiceOptionAutoRequired)),
		}
	}
	return params
}

func (d *OpenAIDriver) chatCompletionsFallback(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	res, err := d.client.Chat.Completions.New(ctx, d.chatCompletionParams(messages))
	if err != nil {
		return d.wrapStreamError("chat.completions", err)
	}
	outputChars := 0
	for _, choice := range res.Choices {
		text := choice.Message.Content
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
	usage := llm.Usage{}
	if res.Usage.PromptTokens > 0 || res.Usage.CompletionTokens > 0 {
		usage.InputTokens = int(res.Usage.PromptTokens)
		usage.OutputTokens = int(res.Usage.CompletionTokens)
	}
	if usage.OutputTokens == 0 && outputChars > 0 {
		usage.OutputTokens = (outputChars + 3) / 4
	}
	d.mu.Lock()
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		d.lastUsage = usage
	}
	d.mu.Unlock()
	return nil
}

func (d *OpenAIDriver) streamResponses(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	params, err := d.responsesParams(ctx, messages)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.lastRequestMode = params.requestMode
	d.mu.Unlock()

	stream := d.client.Responses.NewStreaming(ctx, params.params)
	var responseID string
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
			responseID = event.Response.ID
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
	if responseID != "" && d.shouldPersistResponsesState() {
		d.mu.Lock()
		d.prevResponseID = responseID
		d.lastMessages = append([]llm.Message(nil), messages...)
		d.mu.Unlock()
	}
	return nil
}

type responseParamsResult struct {
	params      responses.ResponseNewParams
	requestMode string
}

func (d *OpenAIDriver) responsesParams(ctx context.Context, messages []llm.Message) (responseParamsResult, error) {
	instructions, inputMessages, previousResponseID, requestMode, err := d.responsesRequestState(ctx, messages)
	if err != nil {
		return responseParamsResult{}, err
	}
	params := responses.ResponseNewParams{
		Model: d.apiModel,
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: toResponseInput(inputMessages),
		},
	}
	if d.providerRequiresStatelessResponses() {
		params.Store = openai.Bool(false)
		params.Include = append(params.Include, responses.ResponseIncludable("reasoning.encrypted_content"))
	} else if providerSupportsResponseStore(d.providerLabel) {
		params.Store = openai.Bool(true)
	}
	if instructions != "" {
		params.Instructions = openai.String(instructions)
	}
	if previousResponseID != "" {
		params.PreviousResponseID = openai.String(previousResponseID)
	}
	params.PromptCacheKey = openai.String(responsePromptCacheKey(d.apiModel, instructions))
	if d.params.MaxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(d.params.MaxTokens))
	}
	if d.params.Temperature >= 0 && d.modelSupportsTemperature() {
		params.Temperature = openai.Float(d.params.Temperature)
	}
	return responseParamsResult{params: params, requestMode: requestMode}, nil
}

func (d *OpenAIDriver) responsesRequestState(ctx context.Context, messages []llm.Message) (instructions string, inputMessages []llm.Message, previousResponseID string, requestMode string, err error) {
	instructions = responseInstructions(messages)
	if d.providerRequiresStatelessResponses() {
		return instructions, stripSystemMessages(messages), "", "responses full input (chatgpt stateless)", nil
	}
	d.mu.Lock()
	prevID := d.prevResponseID
	lastMessages := append([]llm.Message(nil), d.lastMessages...)
	d.mu.Unlock()

	if providerSupportsResponseReuse(d.providerLabel) && prevID != "" && isAppendOnlyMessageHistory(lastMessages, messages) {
		return instructions, stripSystemMessages(messages[len(lastMessages):]), prevID, "responses append-only reuse", nil
	}
	if estimatedMessageTokens(messages) <= responseStateCompactionThreshold {
		return instructions, stripSystemMessages(messages), "", "responses full input", nil
	}
	if !providerSupportsResponseCompaction(d.providerLabel) {
		return instructions, stripSystemMessages(messages), "", "responses full input", nil
	}
	prefix := compactibleMessagePrefix(messages, responseStatePreserveMessages)
	if len(stripSystemMessages(prefix)) < 2 {
		return instructions, stripSystemMessages(messages), "", "responses full input", nil
	}
	compactionID, compactErr := d.compactResponseState(ctx, prefix)
	if compactErr != nil {
		return instructions, stripSystemMessages(messages), "", "responses full input (compact fallback)", nil
	}
	d.mu.Lock()
	d.prevResponseID = compactionID
	d.lastMessages = append([]llm.Message(nil), prefix...)
	d.mu.Unlock()
	return instructions, stripSystemMessages(messages[len(prefix):]), compactionID, "responses native compact", nil
}

func responseInstructions(messages []llm.Message) string {
	var parts []string
	for _, m := range messages {
		if m.Role != llm.RoleSystem {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func stripSystemMessages(messages []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(messages))
	for _, m := range messages {
		if m.Role == llm.RoleSystem {
			continue
		}
		out = append(out, m)
	}
	return out
}

func isAppendOnlyMessageHistory(prev, current []llm.Message) bool {
	if len(current) < len(prev) {
		return false
	}
	for i := range prev {
		p, c := prev[i], current[i]
		if p.Role != c.Role || p.Content != c.Content {
			return false
		}
		if p.ToolCallID != c.ToolCallID {
			return false
		}
		if len(p.ToolCalls) != len(c.ToolCalls) {
			return false
		}
		for j := range p.ToolCalls {
			if p.ToolCalls[j] != c.ToolCalls[j] {
				return false
			}
		}
	}
	return true
}

func compactibleMessagePrefix(messages []llm.Message, preserve int) []llm.Message {
	if len(messages) <= preserve {
		return nil
	}
	cutoff := len(messages) - preserve
	if cutoff <= 0 {
		return nil
	}
	return append([]llm.Message(nil), messages[:cutoff]...)
}

func estimatedMessageTokens(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		total += llm.EstimateTokens(m.Content)
	}
	return total
}

type responseCompaction struct {
	ID string `json:"id"`
}

func (d *OpenAIDriver) compactResponseState(ctx context.Context, prefix []llm.Message) (string, error) {
	body := map[string]any{
		"model": d.apiModel,
		"input": toResponseInput(stripSystemMessages(prefix)),
	}
	var res responseCompaction
	if err := d.client.Post(ctx, "responses/compact", body, &res); err != nil {
		return "", d.wrapStreamError("responses/compact", err)
	}
	if strings.TrimSpace(res.ID) == "" {
		return "", fmt.Errorf("openai stream (api: responses/compact, model: %s): missing compaction response id", d.apiModel)
	}
	return res.ID, nil
}

func responsePromptCacheKey(model, instructions string) string {
	sum := sha256.Sum256([]byte(model + "\n" + strings.TrimSpace(instructions)))
	return "forge:" + model + ":" + hex.EncodeToString(sum[:8])
}

func chatPromptCacheSeed(messages []llm.Message) string {
	if len(messages) == 0 {
		return ""
	}
	limit := len(messages)
	if limit > 4 {
		limit = 4
	}
	var parts []string
	for i := 0; i < limit; i++ {
		text := strings.TrimSpace(messages[i].Content)
		if text == "" {
			continue
		}
		parts = append(parts, string(messages[i].Role)+":"+truncateDebug(text, 200))
	}
	return strings.Join(parts, "\n")
}

func providerHeaders(providerLabel string) []option.RequestOption {
	switch strings.TrimSpace(strings.ToLower(providerLabel)) {
	case "openrouter":
		return []option.RequestOption{
			option.WithHeader("HTTP-Referer", openRouterReferer),
			option.WithHeader("X-Title", openRouterTitle),
			option.WithHeader("X-OpenRouter-Title", openRouterTitle),
		}
	default:
		return nil
	}
}

func providerSupportsStreamUsageOptions(providerLabel string) bool {
	switch strings.TrimSpace(strings.ToLower(providerLabel)) {
	case "anthropic", "chatgpt", "copilot", "openai":
		return true
	default:
		return false
	}
}

func providerUsesLegacyMaxTokensField(providerLabel string) bool {
	switch strings.TrimSpace(strings.ToLower(providerLabel)) {
	case "anthropic", "chatgpt", "copilot", "openai":
		return false
	default:
		return true
	}
}

func providerSupportsResponseStore(providerLabel string) bool {
	switch strings.TrimSpace(strings.ToLower(providerLabel)) {
	case "openai", "chatgpt":
		return true
	default:
		return false
	}
}

func providerSupportsResponseReuse(providerLabel string) bool {
	switch strings.TrimSpace(strings.ToLower(providerLabel)) {
	case "openai", "chatgpt":
		return true
	default:
		return false
	}
}

func providerSupportsResponseCompaction(providerLabel string) bool {
	switch strings.TrimSpace(strings.ToLower(providerLabel)) {
	case "openai", "chatgpt":
		return true
	default:
		return false
	}
}

func (d *OpenAIDriver) shouldFallbackToNonStreaming(err error) bool {
	if err == nil || !providerUsesLegacyMaxTokensField(d.providerLabel) {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "502") || strings.Contains(msg, "503") || strings.Contains(msg, "504") || strings.Contains(msg, "bad gateway") || strings.Contains(msg, "gateway timeout")
}

func toOpenAIMessages(msgs []llm.Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			out = append(out, openai.SystemMessage(m.Content))
		case llm.RoleUser:
			out = append(out, openai.UserMessage(m.Content))
		case llm.RoleTool:
			out = append(out, openai.ToolMessage(m.Content, m.ToolCallID))
		case llm.RoleAssistant:
			if len(m.ToolCalls) > 0 {
				calls := make([]openai.ChatCompletionMessageToolCallParam, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					calls = append(calls, openai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: tc.ArgsJSON,
						},
					})
				}
				assistantMsg := openai.ChatCompletionAssistantMessageParam{
					Content: openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: param.NewOpt(m.Content),
					},
					ToolCalls: calls,
				}
				out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistantMsg})
			} else {
				out = append(out, openai.AssistantMessage(m.Content))
			}
		}
	}
	return out
}

func (d *OpenAIDriver) shouldFallbackAfterEmptyStream() bool {
	return providerUsesLegacyMaxTokensField(d.providerLabel)
}

// toolDefsToOpenAI converts llm.ToolDef slice to OpenAI chat completion tool params.
func toolDefsToOpenAI(defs []llm.ToolDef) []openai.ChatCompletionToolParam {
	tools := make([]openai.ChatCompletionToolParam, 0, len(defs))
	for _, d := range defs {
		properties := make(map[string]any, len(d.Parameters))
		required := make([]string, 0)
		for _, p := range d.Parameters {
			prop := map[string]any{"type": p.Type}
			if p.Description != "" {
				prop["description"] = p.Description
			}
			properties[p.Name] = prop
			if p.Required {
				required = append(required, p.Name)
			}
		}
		schema := map[string]any{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			schema["required"] = required
		}
		tools = append(tools, openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        d.Name,
				Description: param.NewOpt(d.Description),
				Parameters:  shared.FunctionParameters(schema),
			},
		})
	}
	return tools
}

// driverAppendMissingJSONClosers appends missing closing braces/brackets to raw JSON.
// Inlined from internal/agent/parse.go to avoid circular imports.
func driverAppendMissingJSONClosers(raw string) (string, bool) {
	var stack []byte
	inString := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != ch {
				return raw, false
			}
			stack = stack[:len(stack)-1]
		}
	}
	if inString || len(stack) == 0 {
		return raw, false
	}
	var out strings.Builder
	out.WriteString(raw)
	for i := len(stack) - 1; i >= 0; i-- {
		out.WriteByte(stack[i])
	}
	return out.String(), true
}

// driverEscapeBareJSONStringControls escapes unescaped control chars inside JSON strings.
// Inlined from internal/agent/parse.go to avoid circular imports.
func driverEscapeBareJSONStringControls(raw string) (string, bool) {
	var out strings.Builder
	out.Grow(len(raw))
	inString := false
	escaped := false
	changed := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inString {
			if escaped {
				out.WriteByte(ch)
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				out.WriteByte(ch)
				escaped = true
			case '"':
				out.WriteByte(ch)
				inString = false
			case '\n':
				out.WriteString(`\n`)
				changed = true
			case '\r':
				out.WriteString(`\r`)
				changed = true
			case '\t':
				out.WriteString(`\t`)
				changed = true
			default:
				out.WriteByte(ch)
			}
			continue
		}
		out.WriteByte(ch)
		if ch == '"' {
			inString = true
		}
	}
	return out.String(), changed
}

// repairToolCallArgsJSON normalizes potentially malformed JSON from streaming deltas.
// Returns "{}" for empty input, empty string if repair fails.
func repairToolCallArgsJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	if json.Valid([]byte(raw)) {
		return raw
	}
	if fixed, changed := driverAppendMissingJSONClosers(raw); changed && json.Valid([]byte(fixed)) {
		return fixed
	}
	if fixed, changed := driverEscapeBareJSONStringControls(raw); changed && json.Valid([]byte(fixed)) {
		return fixed
	}
	return ""
}

// StreamWithTools implements llm.NativeToolCaller. It passes tool definitions via
// the chat completions `tools` parameter and emits NativeToolCall tokens after
// accumulating all streaming deltas.
func (d *OpenAIDriver) StreamWithTools(ctx context.Context, messages []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	return d.StreamWithToolsOptions(ctx, messages, tools, llm.NativeToolOptions{}, out)
}

func (d *OpenAIDriver) StreamWithToolsOptions(ctx context.Context, messages []llm.Message, tools []llm.ToolDef, opts llm.NativeToolOptions, out chan<- llm.Token) error {
	defer close(out)
	if d.useResponsesAPI() {
		return d.streamResponses(ctx, messages, out)
	}

	params := d.chatCompletionParamsWithTools(messages, opts)
	if len(tools) > 0 {
		params.Tools = toolDefsToOpenAI(tools)
	}

	type accumulator struct {
		id   strings.Builder
		name strings.Builder
		args strings.Builder
	}
	accs := map[int]*accumulator{}

	var outputChars int
	stream := d.client.Chat.Completions.NewStreaming(ctx, params)
	for stream.Next() {
		chunk := stream.Current()
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage := llm.Usage{
				InputTokens:  int(chunk.Usage.PromptTokens),
				OutputTokens: int(chunk.Usage.CompletionTokens),
			}
			d.mu.Lock()
			d.lastUsage = usage
			d.mu.Unlock()
		}
		for _, choice := range chunk.Choices {
			if text := choice.Delta.Content; text != "" {
				outputChars += len(text)
				select {
				case out <- llm.Token{Text: text}:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				idx := int(tc.Index)
				if _, ok := accs[idx]; !ok {
					accs[idx] = &accumulator{}
				}
				a := accs[idx]
				if tc.ID != "" {
					a.id.WriteString(tc.ID)
				}
				if tc.Function.Name != "" {
					a.name.WriteString(tc.Function.Name)
				}
				if tc.Function.Arguments != "" {
					a.args.WriteString(tc.Function.Arguments)
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		if d.shouldFallbackToNonStreaming(err) {
			return d.chatCompletionsFallback(ctx, messages, out)
		}
		return d.wrapStreamError("chat.completions.tools", err)
	}

	for i := 0; i < len(accs); i++ {
		a, ok := accs[i]
		if !ok {
			continue
		}
		name := strings.TrimSpace(a.name.String())
		if name == "" {
			continue
		}
		id := strings.TrimSpace(a.id.String())
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		argsJSON := repairToolCallArgsJSON(a.args.String())
		if argsJSON == "" {
			argsJSON = "{}"
		}
		select {
		case out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: id, Name: name, ArgsJSON: argsJSON}}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	d.mu.Lock()
	if d.lastUsage.OutputTokens == 0 && outputChars > 0 {
		d.lastUsage.OutputTokens = (outputChars + 3) / 4
	}
	d.mu.Unlock()
	return nil
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
	label := strings.TrimSpace(d.providerLabel)
	if label == "" {
		label = "openai"
	}
	msg := fmt.Sprintf("%s stream (api: %s, model: %s): %s", label, api, d.apiModel, normalizeStreamError(err))
	detail := extractHTTPErrorDetails(err)
	if detail == "" {
		return fmt.Errorf("%s", msg)
	}
	return fmt.Errorf("%s [%s]", msg, detail)
}

func normalizeStreamError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.TrimPrefix(msg, "received error while streaming: ")
	msg = strings.TrimSpace(msg)
	return msg
}

func extractHTTPErrorDetails(err error) string {
	if err == nil {
		return ""
	}
	type headerCarrier interface{ Headers() http.Header }
	type bodyCarrier interface{ DumpRequest(bool) ([]byte, error) }
	_ = bodyCarrier(nil)
	parts := make([]string, 0, 6)
	errText := normalizeStreamError(err)
	if errText != "" && !looksLikeStructuredProviderError(errText) {
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

func looksLikeStructuredProviderError(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
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
	return d.forceResponses || (d.supportsResponses && modelRequiresResponses(d.providerLabel, d.apiModel))
}

// modelSupportsTemperature returns true when the given model accepts a
// temperature parameter. It consults the models.dev catalog first; when the
// model is not in the catalog it falls back to the name-based heuristic
// (reasoning models generally don't support temperature).
func (d *OpenAIDriver) modelSupportsTemperature() bool {
	return modelSupportsTemperature(d.providerLabel, d.apiModel)
}

func modelSupportsTemperature(providerLabel, model string) bool {
	if info := modelcatalog.Lookup(providerLabel, model); info != nil {
		return info.Temperature
	}
	return !isReasoningModel(model)
}

func modelRequiresResponses(providerLabel, model string) bool {
	if providerRequiresStatelessResponses(providerLabel, model) {
		return true
	}
	return isReasoningModel(model)
}

func (d *OpenAIDriver) providerRequiresStatelessResponses() bool {
	return providerRequiresStatelessResponses(d.providerLabel, d.apiModel)
}

func (d *OpenAIDriver) shouldPersistResponsesState() bool {
	return providerSupportsResponseReuse(d.providerLabel) && !d.providerRequiresStatelessResponses()
}

func providerRequiresStatelessResponses(providerLabel, model string) bool {
	if strings.TrimSpace(strings.ToLower(providerLabel)) != "chatgpt" {
		return false
	}
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "gpt-5")
}

func isReasoningModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	// Only the exact gpt-5 alias and o-series are true reasoning models that
	// require the Responses API. gpt-5.x variants (ChatGPT/Codex) and
	// gpt-5-mini are regular chat models that work fine via chat completions.
	switch m {
	case "gpt-5", "gpt5":
		return true
	}
	return strings.HasPrefix(m, "o1") ||
		strings.HasPrefix(m, "o3") ||
		strings.HasPrefix(m, "o4")
}
