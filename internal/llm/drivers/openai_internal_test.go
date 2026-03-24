package drivers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forge/internal/llm"
)

func TestModelRequiresResponses(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		// Only the exact gpt-5/gpt5 alias and o-series are reasoning models.
		{model: "gpt-5", want: true},
		{model: "gpt5", want: true},
		{model: "o1", want: true},
		{model: "o1-mini", want: true},
		{model: "o3-mini", want: true},
		{model: "o4-mini", want: true},
		// gpt-5.x variants (ChatGPT/Codex), gpt-5-mini etc. are chat models.
		{model: "gpt-5.4", want: false},
		{model: "gpt-5-mini", want: false},
		{model: "gpt5.1", want: false},
		{model: "gpt5-mini", want: false},
		{model: "gpt-4o", want: false},
		{model: "openai/gpt-5.4", want: false},
	}

	for _, tt := range tests {
		if got := modelRequiresResponses("", tt.model); got != tt.want {
			t.Fatalf("modelRequiresResponses(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestUseResponsesAPI(t *testing.T) {
	tests := []struct {
		name              string
		driver            *OpenAIDriver
		wantUsesResponses bool
	}{
		{
			name:              "openai gpt-5 uses responses",
			driver:            NewOpenAI("sk-test", "gpt-5"),
			wantUsesResponses: true,
		},
		{
			name:              "openai gpt-5.4 chat variant stays on chat completions",
			driver:            NewOpenAI("sk-test", "gpt-5.4"),
			wantUsesResponses: false,
		},
		{
			name:              "chatgpt gpt-5.4 uses responses",
			driver:            &OpenAIDriver{providerLabel: "chatgpt", apiModel: "gpt-5.4", supportsResponses: true},
			wantUsesResponses: true,
		},
		{
			name:              "chatgpt gpt-5.1-codex uses responses",
			driver:            &OpenAIDriver{providerLabel: "chatgpt", apiModel: "gpt-5.1-codex", supportsResponses: true},
			wantUsesResponses: true,
		},
		{
			name:              "openai gpt-4o stays on chat completions",
			driver:            NewOpenAI("sk-test", "gpt-4o"),
			wantUsesResponses: false,
		},
		{
			name:              "openai o3 uses responses",
			driver:            NewOpenAI("sk-test", "o3-mini"),
			wantUsesResponses: true,
		},
		{
			// Copilot API does not support the Responses endpoint; it must
			// always use chat completions regardless of model.
			name:              "copilot always uses chat completions",
			driver:            NewCopilot("gho-test", "copilot/gpt-5.4", "gpt-5.4"),
			wantUsesResponses: false,
		},
		{
			name:              "compat providers stay on chat completions",
			driver:            NewOpenAICompatible("sk-test", "https://example.com/v1", "gpt-5.4"),
			wantUsesResponses: false,
		},
	}

	for _, tt := range tests {
		if got := tt.driver.useResponsesAPI(); got != tt.wantUsesResponses {
			t.Fatalf("%s: useResponsesAPI() = %v, want %v", tt.name, got, tt.wantUsesResponses)
		}
	}
}

func TestModelSupportsTemperature(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-4o", want: true},
		{model: "gpt-4o-mini", want: true},
		// gpt-5.x ChatGPT/Codex variants are chat models that support temperature.
		{model: "gpt-5.4", want: true},
		// Exact gpt-5 and o-series are reasoning models — no temperature.
		{model: "gpt-5", want: false},
		{model: "o1", want: false},
		{model: "o3-mini", want: false},
		{model: "o4-mini", want: false},
	}

	for _, tt := range tests {
		// Use the heuristic path (empty providerLabel means no catalog lookup for
		// these bare model IDs which aren't in the catalog under the default key).
		if got := modelSupportsTemperature("", tt.model); got != tt.want {
			t.Fatalf("modelSupportsTemperature(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestResponseInstructionsCollectsSystemMessages(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system one"},
		{Role: llm.RoleUser, Content: "user"},
		{Role: llm.RoleSystem, Content: "system two"},
	}
	if got := responseInstructions(msgs); got != "system one\n\nsystem two" {
		t.Fatalf("got %q", got)
	}
}

func TestStripSystemMessages(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "user"},
		{Role: llm.RoleAssistant, Content: "assistant"},
	}
	got := stripSystemMessages(msgs)
	if len(got) != 2 || got[0].Role != llm.RoleUser || got[1].Role != llm.RoleAssistant {
		t.Fatalf("got %#v", got)
	}
}

func TestIsAppendOnlyMessageHistory(t *testing.T) {
	prev := []llm.Message{{Role: llm.RoleSystem, Content: "s"}, {Role: llm.RoleUser, Content: "u1"}}
	curr := []llm.Message{{Role: llm.RoleSystem, Content: "s"}, {Role: llm.RoleUser, Content: "u1"}, {Role: llm.RoleAssistant, Content: "a1"}}
	if !isAppendOnlyMessageHistory(prev, curr) {
		t.Fatal("expected append-only history")
	}
	if isAppendOnlyMessageHistory(curr, prev) {
		t.Fatal("expected non-append history")
	}
}

func TestResponsesRequestStateUsesPreviousResponseIDOnlyForAppendOnlyHistory(t *testing.T) {
	d := NewOpenAI("sk-test", "gpt-5.4")
	d.prevResponseID = "resp_123"
	d.lastMessages = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "u1"},
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1"},
	}

	instructions, input, prevID, mode, err := d.responsesRequestState(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if instructions != "sys" {
		t.Fatalf("instructions = %q", instructions)
	}
	if prevID != "resp_123" {
		t.Fatalf("prevID = %q", prevID)
	}
	if mode != "responses append-only reuse" {
		t.Fatalf("mode = %q", mode)
	}
	if len(input) != 1 || input[0].Content != "a1" {
		t.Fatalf("input = %#v", input)
	}
}

func TestResponsesRequestStateFallsBackToFullInputWhenHistoryDiverges(t *testing.T) {
	d := NewOpenAI("sk-test", "gpt-5.4")
	d.prevResponseID = "resp_123"
	d.lastMessages = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "u1"},
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys updated"},
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1"},
	}

	instructions, input, prevID, mode, err := d.responsesRequestState(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if instructions != "sys updated" {
		t.Fatalf("instructions = %q", instructions)
	}
	if prevID != "" {
		t.Fatalf("prevID = %q", prevID)
	}
	if mode == "" {
		t.Fatal("expected request mode")
	}
	if len(input) != 2 {
		t.Fatalf("input = %#v", input)
	}
}

func TestResponsesRequestStateUsesFullInputForChatGPTStatelessMode(t *testing.T) {
	d := &OpenAIDriver{
		providerLabel:     "chatgpt",
		registryName:      "chatgpt/gpt-5.4",
		apiModel:          "gpt-5.4",
		supportsResponses: true,
	}
	d.prevResponseID = "resp_123"
	d.lastMessages = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "u1"},
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1"},
	}

	instructions, input, prevID, mode, err := d.responsesRequestState(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if instructions != "sys" {
		t.Fatalf("instructions = %q", instructions)
	}
	if prevID != "" {
		t.Fatalf("prevID = %q, want empty", prevID)
	}
	if mode != "responses full input (chatgpt stateless)" {
		t.Fatalf("mode = %q", mode)
	}
	if len(input) != 2 {
		t.Fatalf("input = %#v", input)
	}
}

func TestResponseParamsUseStatelessCodexDefaultsForChatGPT(t *testing.T) {
	d := &OpenAIDriver{
		providerLabel:     "chatgpt",
		registryName:      "chatgpt/gpt-5.4",
		apiModel:          "gpt-5.4",
		supportsResponses: true,
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "u1"},
	}

	got, err := d.responsesParams(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	params := got.params
	if !params.Store.Valid() || params.Store.Value != false {
		t.Fatalf("Store = %#v, want explicit false", params.Store)
	}
	found := false
	for _, include := range params.Include {
		if string(include) == "reasoning.encrypted_content" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Include = %#v, want reasoning.encrypted_content", params.Include)
	}
	if params.PreviousResponseID.Valid() {
		t.Fatalf("PreviousResponseID = %#v, want absent", params.PreviousResponseID)
	}
}

func TestResponsePromptCacheKeyStableAndSensitiveToInputs(t *testing.T) {
	a := responsePromptCacheKey("gpt-5.4", "system prompt")
	b := responsePromptCacheKey("gpt-5.4", "system prompt")
	c := responsePromptCacheKey("gpt-5.4", "different prompt")
	if a != b {
		t.Fatalf("expected stable key, got %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("expected different key for different instructions")
	}
}

func TestCompactibleMessagePrefixPreservesRecentMessages(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "s"},
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleUser, Content: "u2"},
		{Role: llm.RoleAssistant, Content: "a2"},
		{Role: llm.RoleUser, Content: "u3"},
		{Role: llm.RoleAssistant, Content: "a3"},
	}
	got := compactibleMessagePrefix(msgs, 3)
	if len(got) != 4 {
		t.Fatalf("got len=%d", len(got))
	}
	if got[0].Content != "s" || got[3].Content != "u2" {
		t.Fatalf("unexpected prefix: %#v", got)
	}
}

func TestEstimatedMessageTokens(t *testing.T) {
	msgs := []llm.Message{{Content: "hello world"}, {Content: "more text here"}}
	if got := estimatedMessageTokens(msgs); got < 4 {
		t.Fatalf("got %d", got)
	}
}

func TestWrapStreamErrorDoesNotDuplicateStructuredProviderErrors(t *testing.T) {
	d := NewOpenAI("sk-test", "gpt-5.4")
	err := d.wrapStreamError("responses", context.Canceled)
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context canceled, got %q", err)
	}

	providerErr := d.wrapStreamError("responses", assertErr(`received error while streaming: {"type":"insufficient_quota","code":"insufficient_quota","message":"quota exceeded"}`))
	got := providerErr.Error()
	if strings.Contains(got, "[err=") {
		t.Fatalf("expected no duplicated err detail, got %q", got)
	}
	if strings.Contains(got, "received error while streaming") {
		t.Fatalf("expected streaming prefix to be normalized, got %q", got)
	}
}

func TestOpenRouterCompatibleDriverAddsProviderHeadersAndCacheKey(t *testing.T) {
	t.Parallel()

	type requestBody struct {
		Model          string `json:"model"`
		PromptCacheKey string `json:"prompt_cache_key"`
	}

	var gotAuth string
	var gotReferer string
	var gotTitle string
	var gotOpenRouterTitle string
	var gotBody requestBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/chat/completions"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		gotAuth = r.Header.Get("Authorization")
		gotReferer = r.Header.Get("HTTP-Referer")
		gotTitle = r.Header.Get("X-Title")
		gotOpenRouterTitle = r.Header.Get("X-OpenRouter-Title")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	d := NewOpenAICompatibleProviderAlias("openrouter", "sk-test", srv.URL, "openrouter/minimax/minimax-m2.5:free", "minimax/minimax-m2.5:free")
	out := make(chan llm.Token, 4)
	if err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "test"}}, out); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	for range out {
	}

	if got, want := gotAuth, "Bearer sk-test"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotReferer, openRouterReferer; got != want {
		t.Fatalf("HTTP-Referer = %q, want %q", got, want)
	}
	if got, want := gotTitle, openRouterTitle; got != want {
		t.Fatalf("X-Title = %q, want %q", got, want)
	}
	if got, want := gotOpenRouterTitle, openRouterTitle; got != want {
		t.Fatalf("X-OpenRouter-Title = %q, want %q", got, want)
	}
	if got, want := gotBody.Model, "minimax/minimax-m2.5:free"; got != want {
		t.Fatalf("model = %q, want %q", got, want)
	}
	if strings.TrimSpace(gotBody.PromptCacheKey) == "" {
		t.Fatal("expected prompt_cache_key to be set")
	}
}

func TestCopilotResponsesOmitsStoreFlag(t *testing.T) {
	t.Parallel()

	// Copilot uses chat completions, not the Responses API. Verify it hits the
	// correct path and never sends the store flag (which is Responses-API-only).
	type requestBody struct {
		Model string `json:"model"`
		Store *bool  `json:"store,omitempty"`
	}

	var body requestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/chat/completions"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	d := newOpenAI("gho-test", "copilot", "copilot/gpt-5.4-mini", "gpt-5.4-mini", false, srv.URL, srv.Client())
	out := make(chan llm.Token, 8)
	if err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	for range out {
	}

	if body.Store != nil {
		t.Fatalf("expected store to be omitted for copilot, got %v", *body.Store)
	}
}

func TestResponsesStateSkipsNativeCompactionForNonCompactionProviders(t *testing.T) {
	// Copilot is a non-compaction provider. Even if wired up to use the
	// Responses API internally (e.g. via newOpenAI directly), the request
	// state logic must not attempt native compaction.
	d := newOpenAI("gho-test", "copilot", "copilot/gpt-5.4", "gpt-5.4", true, "", nil)
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: strings.Repeat("s", 4000)},
		{Role: llm.RoleUser, Content: strings.Repeat("u", 4000)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("a", 4000)},
	}

	_, _, prevID, mode, err := d.responsesRequestState(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if prevID != "" {
		t.Fatalf("prevID = %q, want empty", prevID)
	}
	if mode != "responses full input" {
		t.Fatalf("mode = %q, want responses full input", mode)
	}
}

func TestNVIDIACompatibleDriverOmitsStreamUsageOptions(t *testing.T) {
	t.Parallel()

	type requestBody struct {
		Model               string          `json:"model"`
		StreamOptions       json.RawMessage `json:"stream_options"`
		MaxTokens           *int64          `json:"max_tokens"`
		MaxCompletionTokens *int64          `json:"max_completion_tokens"`
	}

	var gotBody requestBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/chat/completions"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	d := NewOpenAICompatibleProviderAlias("nvidia", "nvapi-test", srv.URL, "nvidia/meta/llama-3.3-70b-instruct", "meta/llama-3.3-70b-instruct")
	d.SetParams(llm.Params{MaxTokens: 123})
	out := make(chan llm.Token, 4)
	if err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "test"}}, out); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	for range out {
	}

	if got, want := gotBody.Model, "meta/llama-3.3-70b-instruct"; got != want {
		t.Fatalf("model = %q, want %q", got, want)
	}
	if len(gotBody.StreamOptions) != 0 {
		t.Fatalf("expected no stream_options for nvidia, got %s", string(gotBody.StreamOptions))
	}
	if gotBody.MaxTokens == nil || *gotBody.MaxTokens != 123 {
		t.Fatalf("expected max_tokens=123, got %#v", gotBody.MaxTokens)
	}
	if gotBody.MaxCompletionTokens != nil {
		t.Fatalf("expected no max_completion_tokens for nvidia, got %#v", gotBody.MaxCompletionTokens)
	}
}

func TestCompatibleProviderFallsBackToNonStreamingOnGatewayErrors(t *testing.T) {
	t.Parallel()

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if len(calls) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`bad gateway`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"deepseek-ai/deepseek-r1-0528","choices":[{"index":0,"finish_reason":"stop","logprobs":{"content":[],"refusal":[]},"message":{"role":"assistant","content":"hello","refusal":""}}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
	}))
	defer srv.Close()

	d := NewOpenAICompatibleProviderAlias("nvidia", "nvapi-test", srv.URL, "nvidia/deepseek-ai/deepseek-r1-0528", "deepseek-ai/deepseek-r1-0528")
	out := make(chan llm.Token, 4)
	if err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "test"}}, out); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	var parts []string
	for tok := range out {
		parts = append(parts, tok.Text)
	}
	if got := strings.Join(parts, ""); got != "hello" {
		t.Fatalf("output = %q, want %q", got, "hello")
	}
	if len(calls) < 2 {
		t.Fatalf("calls = %#v, want fallback to trigger an additional request", calls)
	}
}

func TestCustomCompatProviderSendsCustomHeaders(t *testing.T) {
	t.Parallel()

	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	headers := map[string]string{
		"X-Custom-Auth":  "token-abc",
		"X-Workspace-ID": "ws-123",
	}
	d := NewCustomCompatProvider("mycorp", "sk-test", srv.URL, "mycorp/my-model", "my-model", false, headers)
	out := make(chan llm.Token, 4)
	if err := d.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, out); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	for range out {
	}

	if got := gotHeaders.Get("X-Custom-Auth"); got != "token-abc" {
		t.Fatalf("X-Custom-Auth = %q, want %q", got, "token-abc")
	}
	if got := gotHeaders.Get("X-Workspace-ID"); got != "ws-123" {
		t.Fatalf("X-Workspace-ID = %q, want %q", got, "ws-123")
	}
	if got := gotHeaders.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer sk-test")
	}
}

func TestCustomCompatProviderResponsesModeEnabled(t *testing.T) {
	d := NewCustomCompatProvider("mycorp", "sk-test", "https://api.mycorp.com/v1", "mycorp/o3-mini", "o3-mini", true, nil)
	if !d.useResponsesAPI() {
		t.Fatal("expected useResponsesAPI() = true for custom provider with supportsResponses=true and reasoning model")
	}
}

func TestCustomCompatProviderResponsesModeForcesChatModels(t *testing.T) {
	d := NewCustomCompatProvider("mycorp", "sk-test", "https://api.mycorp.com/v1", "mycorp/gpt-5.4-pro", "gpt-5.4-pro", true, nil)
	if !d.useResponsesAPI() {
		t.Fatal("expected useResponsesAPI() = true for custom provider with wire_api=responses")
	}
}

func TestCustomCompatProviderResponsesModeDisabledByDefault(t *testing.T) {
	// Default compat providers (via NewOpenAICompatibleProviderAlias) never support responses.
	d := NewOpenAICompatibleProviderAlias("someprovider", "sk-test", "https://example.com/v1", "someprovider/o3-mini", "o3-mini")
	if d.useResponsesAPI() {
		t.Fatal("expected useResponsesAPI() = false for default compat provider")
	}
}

func TestCustomCompatProviderNoStatelessResponses(t *testing.T) {
	// Custom providers must NOT inherit ChatGPT-specific stateless responses behavior.
	d := NewCustomCompatProvider("mycorp", "sk-test", "https://api.mycorp.com/v1", "mycorp/gpt-5.4", "gpt-5.4", true, nil)
	if d.providerRequiresStatelessResponses() {
		t.Fatal("custom provider should not require stateless responses")
	}
}

func TestCustomCompatProviderNoResponseStore(t *testing.T) {
	// Custom providers must NOT get response store (only "openai" and "chatgpt" do).
	if providerSupportsResponseStore("mycorp") {
		t.Fatal("custom provider should not support response store")
	}
}

func TestCustomCompatProviderNoResponseCompaction(t *testing.T) {
	// Custom providers must NOT get response compaction (only "openai" and "chatgpt" do).
	if providerSupportsResponseCompaction("mycorp") {
		t.Fatal("custom provider should not support response compaction")
	}
}

func TestCustomCompatProviderResponsesParamsNoStoreNoStateless(t *testing.T) {
	// When a custom provider uses responses mode, verify the params don't set
	// Store=false (ChatGPT stateless) or Store=true (OpenAI store).
	d := NewCustomCompatProvider("mycorp", "sk-test", "https://api.mycorp.com/v1", "mycorp/o3-mini", "o3-mini", true, nil)
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
	}
	got, err := d.responsesParams(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if got.params.Store.Valid() {
		t.Fatalf("Store = %v, want unset for custom provider", got.params.Store.Value)
	}
	for _, inc := range got.params.Include {
		if string(inc) == "reasoning.encrypted_content" {
			t.Fatal("custom provider should not include reasoning.encrypted_content")
		}
	}
}

func assertErr(msg string) error { return simpleErr(msg) }

type simpleErr string

func (e simpleErr) Error() string { return string(e) }
