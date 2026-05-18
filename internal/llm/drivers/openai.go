package drivers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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

	"github.com/gorilla/websocket"
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

	wsConn          *websocket.Conn
	wsMu            sync.Mutex
	wsFallbackHTTP  bool
	wsBaseURL       string
	wsAuthManager   *chatgptauth.Manager // non-nil for ChatGPT provider
	wsLastRequestID string
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
	wsURL := wsBaseURLFromHTTP(baseURL)
	return &OpenAIDriver{
		client:            &client,
		providerLabel:     providerLabel,
		registryName:      registryName,
		apiModel:          apiModel,
		supportsResponses: supportsResponses,
		params:            llm.Params{Temperature: -1},
		wsBaseURL:         wsURL,
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
	d := newOpenAI("chatgpt-oauth", "chatgpt", registryName, apiModel, true, authMgr.BaseURL(), authMgr.HTTPClient())
	d.wsAuthManager = authMgr
	return d
}

func wsBaseURLFromHTTP(baseURL string) string {
	b := strings.TrimSpace(baseURL)
	if b == "" {
		return ""
	}
	b = strings.TrimRight(b, "/")
	b = strings.TrimPrefix(b, "https://")
	b = strings.TrimPrefix(b, "http://")
	return "wss://" + b + "/responses"
}

func (d *OpenAIDriver) Close() {
	d.wsMu.Lock()
	defer d.wsMu.Unlock()
	if d.wsConn != nil {
		_ = d.wsConn.Close()
		d.wsConn = nil
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

func (d *OpenAIDriver) LastRequestMode() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastRequestMode
}

func (d *OpenAIDriver) ResetConversation() {
	d.mu.Lock()
	d.prevResponseID = ""
	d.lastMessages = nil
	d.lastRequestMode = ""
	d.mu.Unlock()
	d.wsMu.Lock()
	d.wsLastRequestID = ""
	if d.wsFallbackHTTP {
		d.wsFallbackHTTP = false
	}
	d.wsMu.Unlock()
	d.wsDisconnect()
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
	var reasoningChars int
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
			if rc := extractReasoningContent(chunk.RawJSON()); rc != "" {
				reasoningChars += len(rc)
				select {
				case out <- llm.Token{ReasoningContent: rc}:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
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
		Messages: toOpenAIMessages(messages, d.requiresAssistantReasoningContent()),
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
	if opts.RequireToolCall && d.supportsRequiredChatToolChoice() {
		params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openai.Opt(string(openai.ChatCompletionToolChoiceOptionAutoRequired)),
		}
	}
	return params
}

func (d *OpenAIDriver) supportsRequiredChatToolChoice() bool {
	return providerSupportsRequiredChatToolChoice(d.providerLabel, d.registryName, d.apiModel)
}

func (d *OpenAIDriver) requiresAssistantReasoningContent() bool {
	return providerRequiresAssistantReasoningContent(d.providerLabel, d.registryName, d.apiModel)
}

func providerSupportsRequiredChatToolChoice(providerLabel, registryName, apiModel string) bool {
	provider := strings.TrimSpace(strings.ToLower(providerLabel))
	if provider == "opencode-go" {
		if cap, ok := modelcatalog.OpenCodeGoModelCapabilityFor(openCodeGoCapabilityModel(registryName, apiModel)); ok {
			return cap.SupportsRequiredChatToolChoice
		}
	}
	return true
}

func providerRequiresAssistantReasoningContent(providerLabel, registryName, apiModel string) bool {
	provider := strings.TrimSpace(strings.ToLower(providerLabel))
	if provider != "opencode-go" {
		return false
	}
	cap, ok := modelcatalog.OpenCodeGoModelCapabilityFor(openCodeGoCapabilityModel(registryName, apiModel))
	return ok && cap.InterleavedReasoningField == "reasoning_content"
}

func openCodeGoCapabilityModel(registryName, apiModel string) string {
	registryName = strings.TrimSpace(registryName)
	if strings.HasPrefix(registryName, "opencode-go/") {
		return registryName
	}
	if _, ok := modelcatalog.OpenCodeGoModelCapabilityFor(registryName); ok {
		return registryName
	}
	return strings.TrimSpace(apiModel)
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
	return d.streamResponsesWithTools(ctx, messages, nil, llm.NativeToolOptions{}, out)
}

func (d *OpenAIDriver) streamResponsesWithTools(ctx context.Context, messages []llm.Message, tools []llm.ToolDef, opts llm.NativeToolOptions, out chan<- llm.Token) error {
	if d.wsAvailable() && !d.wsFallbackHTTP {
		err := d.wsStreamResponses(ctx, messages, tools, opts, out)
		if err == nil {
			d.mu.Lock()
			d.lastMessages = append([]llm.Message(nil), messages...)
			d.mu.Unlock()
			return nil
		}
		d.wsFallbackHTTP = true
		d.wsDisconnect()
	}

	params, err := d.responsesParamsWithTools(ctx, messages, tools, opts)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.lastRequestMode = params.requestMode
	d.mu.Unlock()

	stream := d.client.Responses.NewStreaming(ctx, params.params)
	var responseID string
	var outputChars int
	var completedOutput []responses.ResponseOutputItemUnion
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
			outputChars += len(event.Delta)
			select {
			case out <- llm.Token{Text: event.Delta}:
			case <-ctx.Done():
				return ctx.Err()
			}
		case responses.ResponseOutputItemDoneEvent:
			completedOutput = append(completedOutput, event.Item)
		case responses.ResponseCompletedEvent:
			responseID = event.Response.ID
			if len(event.Response.Output) > 0 {
				completedOutput = append(completedOutput, event.Response.Output...)
			}
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
		if d.shouldFallbackResponsesToolsToChat(err, len(tools) > 0, outputChars, len(completedOutput), responseID) {
			d.mu.Lock()
			d.lastRequestMode = params.requestMode + " -> chat.completions native tools fallback"
			d.mu.Unlock()
			return d.streamChatCompletionsWithTools(ctx, messages, tools, opts, out)
		}
		return d.wrapStreamError("responses", err)
	}
	if err := emitResponsesFunctionCalls(ctx, out, completedOutput); err != nil {
		return err
	}
	if responseID != "" && d.shouldPersistResponsesState() {
		d.mu.Lock()
		d.prevResponseID = responseID
		d.lastMessages = append([]llm.Message(nil), messages...)
		d.mu.Unlock()
	}
	d.mu.Lock()
	if d.lastUsage.OutputTokens == 0 && outputChars > 0 {
		d.lastUsage.OutputTokens = (outputChars + 3) / 4
	}
	d.mu.Unlock()
	return nil
}

type responseParamsResult struct {
	params      responses.ResponseNewParams
	requestMode string
}

func (d *OpenAIDriver) responsesParams(ctx context.Context, messages []llm.Message) (responseParamsResult, error) {
	return d.responsesParamsWithTools(ctx, messages, nil, llm.NativeToolOptions{})
}

func (d *OpenAIDriver) responsesParamsWithTools(ctx context.Context, messages []llm.Message, tools []llm.ToolDef, opts llm.NativeToolOptions) (responseParamsResult, error) {
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
	if len(tools) > 0 {
		params.Tools = toolDefsToResponses(tools)
		if opts.RequireToolCall {
			params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
				OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsRequired),
			}
		}
	}
	return responseParamsResult{params: params, requestMode: requestMode}, nil
}

func (d *OpenAIDriver) responsesRequestState(ctx context.Context, messages []llm.Message) (instructions string, inputMessages []llm.Message, previousResponseID string, requestMode string, err error) {
	instructions = responseInstructions(messages)
	if d.providerRequiresStatelessResponses() {
		if estimatedMessageTokens(messages) <= responseStateCompactionThreshold {
			return instructions, stripSystemMessages(messages), "", "responses full input (chatgpt stateless)", nil
		}
		trimmed := trimStatelessConversation(messages)
		return instructions, stripSystemMessages(trimmed), "", "responses trimmed input (chatgpt stateless)", nil
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
	h := sha256.Sum256([]byte(model + "\x00" + instructions))
	return hex.EncodeToString(h[:])
}

func trimStatelessConversation(messages []llm.Message) []llm.Message {
	const maxNonSystem = 16
	var system, nonSystem []llm.Message
	for _, m := range messages {
		if m.Role == llm.RoleSystem {
			system = append(system, m)
		} else {
			nonSystem = append(nonSystem, m)
		}
	}
	if len(nonSystem) <= maxNonSystem {
		return messages
	}
	// Walk backwards keeping at least maxNonSystem, expanding to include paired tool
	// calls and tool results so the Responses API doesn't reject orphaned items.
	keptStart := len(nonSystem) - maxNonSystem
	pending := make(map[string]bool) // tool call IDs referenced by kept messages
	for i := len(nonSystem) - 1; i >= keptStart; i-- {
		addCallIDs(pending, nonSystem[i])
	}
	for i := keptStart - 1; i >= 0; i-- {
		m := nonSystem[i]
		if refersToPending(pending, m) {
			keptStart = i
			addCallIDs(pending, m)
		}
	}
	kept := nonSystem[keptStart:]
	latestUserIndex := -1
	for i, msg := range nonSystem {
		if msg.Role == llm.RoleUser {
			latestUserIndex = i
		}
	}
	var latestUser *llm.Message
	if latestUserIndex >= 0 && latestUserIndex < keptStart {
		msg := nonSystem[latestUserIndex]
		latestUser = &msg
	}
	dropped := len(messages) - len(system) - len(kept)
	if latestUser != nil {
		dropped--
	}
	result := make([]llm.Message, 0, len(system)+len(kept)+2)
	result = append(result, system...)
	if dropped > 0 {
		result = append(result, llm.Message{
			Role:    llm.RoleUser,
			Content: fmt.Sprintf("[%d earlier messages trimmed to fit context budget]", dropped),
		})
	}
	if latestUser != nil {
		result = append(result, *latestUser)
	}
	result = append(result, kept...)
	return result
}

func addCallIDs(pending map[string]bool, m llm.Message) {
	if m.Role == llm.RoleAssistant {
		for _, tc := range m.ToolCalls {
			if id := strings.TrimSpace(tc.ID); id != "" {
				pending[id] = true
			}
		}
	}
	if m.Role == llm.RoleTool {
		if id := strings.TrimSpace(m.ToolCallID); id != "" {
			pending[id] = true
		}
	}
}

func refersToPending(pending map[string]bool, m llm.Message) bool {
	if m.Role == llm.RoleAssistant {
		for _, tc := range m.ToolCalls {
			if pending[strings.TrimSpace(tc.ID)] {
				return true
			}
		}
	}
	if m.Role == llm.RoleTool {
		return pending[strings.TrimSpace(m.ToolCallID)]
	}
	return false
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

func toOpenAIMessages(msgs []llm.Message, includeEmptyAssistantReasoning bool) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			out = append(out, openai.SystemMessage(m.Content))
		case llm.RoleUser:
			if m.HasContentParts() {
				parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(m.ContentParts)+1)
				if strings.TrimSpace(m.Content) != "" {
					parts = append(parts, openai.TextContentPart(m.Content))
				}
				for _, part := range m.ContentParts {
					switch part.Type {
					case "text":
						if strings.TrimSpace(part.Text) != "" {
							parts = append(parts, openai.TextContentPart(part.Text))
						}
					case "image":
						if part.Image != nil {
							dataURL, err := imageToDataURL(part.Image.Path, part.Image.MIMEType)
							if err != nil {
								continue
							}
							parts = append(parts, openai.ImageContentPart(
								openai.ChatCompletionContentPartImageImageURLParam{
									URL:    dataURL,
									Detail: "auto",
								}))
						}
					}
				}
				if len(parts) == 0 {
					parts = append(parts, openai.TextContentPart(m.Content))
				}
				out = append(out, openai.UserMessage(parts))
			} else {
				out = append(out, openai.UserMessage(m.Content))
			}
		case llm.RoleTool:
			out = append(out, openai.ToolMessage(m.Content, m.ToolCallID))
		case llm.RoleAssistant:
			reasoningContent := assistantReplayReasoningContent(m, includeEmptyAssistantReasoning)
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
				if reasoningContent != "" {
					assistantMsg.SetExtraFields(map[string]any{"reasoning_content": reasoningContent})
				}
				out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistantMsg})
			} else {
				if reasoningContent != "" {
					amsg := openai.ChatCompletionAssistantMessageParam{
						Content: openai.ChatCompletionAssistantMessageParamContentUnion{
							OfString: param.NewOpt(m.Content),
						},
					}
					amsg.SetExtraFields(map[string]any{"reasoning_content": reasoningContent})
					out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &amsg})
				} else {
					out = append(out, openai.AssistantMessage(m.Content))
				}
			}
		}
	}
	return out
}

func assistantReplayReasoningContent(m llm.Message, required bool) string {
	if strings.TrimSpace(m.ReasoningContent) != "" {
		return m.ReasoningContent
	}
	if !required {
		return ""
	}
	return "No reasoning content was emitted for this assistant turn."
}

func extractReasoningContent(raw string) string {
	if raw == "" {
		return ""
	}
	var chunk struct {
		Choices []struct {
			Delta struct {
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		return ""
	}
	for _, ch := range chunk.Choices {
		if ch.Delta.ReasoningContent != "" {
			return ch.Delta.ReasoningContent
		}
	}
	return ""
}

func (d *OpenAIDriver) shouldFallbackAfterEmptyStream() bool {
	return providerUsesLegacyMaxTokensField(d.providerLabel)
}

// toolDefsToOpenAI converts llm.ToolDef slice to OpenAI chat completion tool params.
func toolDefsToOpenAI(defs []llm.ToolDef) []openai.ChatCompletionToolParam {
	tools := make([]openai.ChatCompletionToolParam, 0, len(defs))
	for _, d := range defs {
		schema := toolDefSchema(d)
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

func toolDefsToResponses(defs []llm.ToolDef) []responses.ToolUnionParam {
	tools := make([]responses.ToolUnionParam, 0, len(defs))
	for _, d := range defs {
		schema := strictToolDefSchema(d)
		tools = append(tools, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        d.Name,
				Description: param.NewOpt(d.Description),
				Parameters:  schema,
				Strict:      openai.Bool(true),
			},
		})
	}
	return tools
}

func strictToolDefSchema(def llm.ToolDef) map[string]any {
	schema := toolDefSchema(def)
	makeSchemaStrictNullable(schema)
	return schema
}

func makeSchemaStrictNullable(schema map[string]any) {
	if schema == nil {
		return
	}
	if items, ok := schema["items"].(map[string]any); ok {
		makeSchemaStrictNullable(items)
	}
	if schemaTypeAllowsObject(schema["type"]) {
		if _, ok := schema["properties"]; !ok {
			schema["properties"] = map[string]any{}
		}
		if _, ok := schema["additionalProperties"]; !ok {
			schema["additionalProperties"] = false
		}
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return
	}
	if len(properties) == 0 {
		schema["required"] = []string{}
		return
	}
	requiredSet := map[string]struct{}{}
	if required, ok := schema["required"].([]string); ok {
		for _, name := range required {
			requiredSet[name] = struct{}{}
		}
	}
	required := make([]string, 0, len(properties))
	for name, rawProp := range properties {
		required = append(required, name)
		prop, ok := rawProp.(map[string]any)
		if !ok {
			continue
		}
		if _, wasRequired := requiredSet[name]; !wasRequired {
			makeSchemaTypeNullable(prop)
		}
		makeSchemaStrictNullable(prop)
	}
	sort.Strings(required)
	schema["required"] = required
	if _, ok := schema["additionalProperties"]; !ok {
		schema["additionalProperties"] = false
	}
}

func schemaTypeAllowsObject(value any) bool {
	switch typ := value.(type) {
	case string:
		return typ == "object"
	case []any:
		for _, item := range typ {
			if item == "object" {
				return true
			}
		}
	case []string:
		for _, item := range typ {
			if item == "object" {
				return true
			}
		}
	}
	return false
}

func makeSchemaTypeNullable(schema map[string]any) {
	if schema == nil {
		return
	}
	switch typ := schema["type"].(type) {
	case string:
		if typ != "null" {
			schema["type"] = []string{typ, "null"}
		}
	case []any:
		for _, item := range typ {
			if item == "null" {
				return
			}
		}
		schema["type"] = append(typ, "null")
	case []string:
		for _, item := range typ {
			if item == "null" {
				makeSchemaEnumNullable(schema)
				return
			}
		}
		schema["type"] = append(typ, "null")
	}
	makeSchemaEnumNullable(schema)
}

func makeSchemaEnumNullable(schema map[string]any) {
	switch enum := schema["enum"].(type) {
	case []any:
		for _, item := range enum {
			if item == nil {
				return
			}
		}
		schema["enum"] = append(enum, nil)
	case []string:
		values := make([]any, 0, len(enum)+1)
		for _, item := range enum {
			values = append(values, item)
		}
		schema["enum"] = append(values, nil)
	}
}

func providerSupportsResponseFunctionTools(providerLabel string) bool {
	switch strings.TrimSpace(strings.ToLower(providerLabel)) {
	case "openai", "chatgpt":
		return true
	default:
		return false
	}
}

func toolDefSchema(def llm.ToolDef) map[string]any {
	if def.Schema != nil {
		return toolSchemaToOpenAI(def.Schema)
	}
	properties := make(map[string]any, len(def.Parameters))
	required := make([]string, 0, len(def.Parameters))
	for _, p := range def.Parameters {
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
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
	return schema
}

func toolSchemaToOpenAI(schema *llm.ToolSchema) map[string]any {
	if schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	}
	out := map[string]any{}
	if schema.Type != "" {
		out["type"] = schema.Type
	}
	if schema.Description != "" {
		out["description"] = schema.Description
	}
	if len(schema.Properties) > 0 {
		props := make(map[string]any, len(schema.Properties))
		for name, prop := range schema.Properties {
			props[name] = toolSchemaToOpenAI(prop)
		}
		out["properties"] = props
	}
	if schema.Items != nil {
		out["items"] = toolSchemaToOpenAI(schema.Items)
	}
	if len(schema.Required) > 0 {
		out["required"] = append([]string(nil), schema.Required...)
	}
	if len(schema.Enum) > 0 {
		out["enum"] = append([]string(nil), schema.Enum...)
	}
	if schema.AdditionalProperties != nil {
		out["additionalProperties"] = *schema.AdditionalProperties
	}
	return out
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
// Returns "{}" for empty input and preserves unrepairable input so execution can
// report the real malformed-arguments error instead of silently dropping args.
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
	return raw
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
		return d.streamResponsesWithTools(ctx, messages, tools, opts, out)
	}
	return d.streamChatCompletionsWithTools(ctx, messages, tools, opts, out)
}

func (d *OpenAIDriver) streamChatCompletionsWithTools(ctx context.Context, messages []llm.Message, tools []llm.ToolDef, opts llm.NativeToolOptions, out chan<- llm.Token) error {
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
			if rc := extractReasoningContent(chunk.RawJSON()); rc != "" {
				select {
				case out <- llm.Token{ReasoningContent: rc}:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
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
				if tc.ID != "" && a.id.Len() == 0 {
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

func (d *OpenAIDriver) shouldFallbackResponsesToolsToChat(err error, hasTools bool, outputChars, outputItems int, responseID string) bool {
	if err == nil || !hasTools {
		return false
	}
	if providerSupportsResponseFunctionTools(d.providerLabel) {
		return false
	}
	if outputChars > 0 || outputItems > 0 || strings.TrimSpace(responseID) != "" {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "400"),
		strings.Contains(msg, "404"),
		strings.Contains(msg, "405"),
		strings.Contains(msg, "422"),
		strings.Contains(msg, "500"),
		strings.Contains(msg, "501"),
		strings.Contains(msg, "502"),
		strings.Contains(msg, "503"),
		strings.Contains(msg, "504"),
		strings.Contains(msg, "bad gateway"),
		strings.Contains(msg, "unsupported"),
		strings.Contains(msg, "not implemented"):
		return true
	default:
		return false
	}
}

func toResponseInput(msgs []llm.Message) []responses.ResponseInputItemUnionParam {
	out := make([]responses.ResponseInputItemUnionParam, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			out = append(out, responses.ResponseInputItemParamOfMessage(m.Content, responses.EasyInputMessageRoleSystem))
		case llm.RoleUser:
			if m.HasContentParts() {
				out = append(out, messageWithContentParts(m, responses.EasyInputMessageRoleUser))
			} else {
				out = append(out, responses.ResponseInputItemParamOfMessage(m.Content, responses.EasyInputMessageRoleUser))
			}
		case llm.RoleTool:
			if strings.TrimSpace(m.ToolCallID) == "" {
				continue
			}
			out = append(out, responses.ResponseInputItemParamOfFunctionCallOutput(m.ToolCallID, m.Content))
		case llm.RoleAssistant:
			if strings.TrimSpace(m.Content) != "" {
				out = append(out, responses.ResponseInputItemParamOfMessage(m.Content, responses.EasyInputMessageRoleAssistant))
			}
			for _, tc := range m.ToolCalls {
				id := strings.TrimSpace(tc.ID)
				if id == "" {
					continue
				}
				out = append(out, responses.ResponseInputItemParamOfFunctionCall(tc.ArgsJSON, id, tc.Name))
			}
		}
	}
	return out
}

func messageWithContentParts(m llm.Message, role responses.EasyInputMessageRole) responses.ResponseInputItemUnionParam {
	parts := make(responses.ResponseInputMessageContentListParam, 0, len(m.ContentParts)+2)
	hasText := false

	// Always include the text content as the first part
	if strings.TrimSpace(m.Content) != "" {
		parts = append(parts, responses.ResponseInputContentParamOfInputText(m.Content))
		hasText = true
	}

	for _, part := range m.ContentParts {
		switch part.Type {
		case "text":
			if strings.TrimSpace(part.Text) != "" {
				parts = append(parts, responses.ResponseInputContentParamOfInputText(part.Text))
				hasText = true
			}
		case "image":
			if part.Image != nil {
				dataURL, err := imageToDataURL(part.Image.Path, part.Image.MIMEType)
				if err != nil {
					continue
				}
				parts = append(parts, responses.ResponseInputContentUnionParam{
					OfInputImage: &responses.ResponseInputImageParam{
						Detail:   responses.ResponseInputImageDetailAuto,
						ImageURL: openai.String(dataURL),
					},
				})
			}
		}
	}
	// Ensure at least one text part exists: the Responses API requires at least
	// one input_text content part in a mixed-content user message.
	if !hasText && len(parts) > 0 {
		parts = append(responses.ResponseInputMessageContentListParam{
			responses.ResponseInputContentParamOfInputText(" "),
		}, parts...)
		hasText = true
	}
	if !hasText {
		return responses.ResponseInputItemParamOfMessage(" ", role)
	}
	return responses.ResponseInputItemParamOfMessage(parts, role)
}

func imageToDataURL(path, mimeType string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read image file %s: %w", path, err)
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded), nil
}

func emitResponsesFunctionCalls(ctx context.Context, out chan<- llm.Token, items []responses.ResponseOutputItemUnion) error {
	for i, item := range items {
		call, ok := item.AsAny().(responses.ResponseFunctionToolCall)
		if !ok {
			continue
		}
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		id := strings.TrimSpace(call.CallID)
		if id == "" {
			id = strings.TrimSpace(call.ID)
		}
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		argsJSON := repairToolCallArgsJSON(call.Arguments)
		if argsJSON == "" {
			argsJSON = "{}"
		}
		select {
		case out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: id, Name: name, ArgsJSON: argsJSON}}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
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
	if idx := strings.LastIndex(m, "/"); idx >= 0 {
		m = m[idx+1:]
	}
	switch m {
	case "gpt-5", "gpt5", "gpt-5.4", "gpt5.4", "gpt-5.5", "gpt5.5":
		return true
	}
	return strings.HasPrefix(m, "o1") ||
		strings.HasPrefix(m, "o3") ||
		strings.HasPrefix(m, "o4")
}
