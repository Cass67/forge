package drivers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
		{model: "gpt-5.4", want: true},
		{model: "gpt-5-mini", want: false},
		{model: "gpt5.1", want: false},
		{model: "gpt5-mini", want: false},
		{model: "gpt-4o", want: false},
		{model: "openai/gpt-5.4", want: true},
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
			name:              "openai gpt-5.4 uses responses (reasoning model)",
			driver:            NewOpenAI("sk-test", "gpt-5.4"),
			wantUsesResponses: true,
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
			driver:            NewOpenAICompatibleProviderAlias("compat", "sk-test", "https://example.com/v1", "gpt-5.4", "gpt-5.4"),
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
		// gpt-5.4 is a reasoning model — no temperature.
		{model: "gpt-5.4", want: false},
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

func TestCoalesceSystemMessages(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "one"},
		{Role: llm.RoleSystem, Content: "two"},
		{Role: llm.RoleSystem, Content: "three"},
		{Role: llm.RoleUser, Content: "user"},
		{Role: llm.RoleSystem, Content: "late"},
	}
	got := coalesceSystemMessages(msgs)
	if len(got) != 3 {
		t.Fatalf("got %d messages: %#v", len(got), got)
	}
	if got[0].Role != llm.RoleSystem || got[0].Content != "one\n\ntwo\n\nthree" {
		t.Fatalf("merged content %q", got[0].Content)
	}
	if got[1].Role != llm.RoleUser || got[1].Content != "user" {
		t.Fatalf("got %#v", got[1])
	}
	if got[2].Role != llm.RoleUser || got[2].Content != "[system note]\nlate" {
		t.Fatalf("mid-history system not demoted: %#v", got[2])
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

func TestResponsesRequestStateUsesFullInputForCustomResponsesProvider(t *testing.T) {
	d := NewCustomCompatProvider("mycorp", "sk-test", "https://api.mycorp.com/v1", "mycorp/gpt-5.4", "gpt-5.4", true, nil)
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
		t.Fatalf("prevID = %q, want empty for custom provider", prevID)
	}
	if mode != "responses full input" {
		t.Fatalf("mode = %q", mode)
	}
	if len(input) != 2 {
		t.Fatalf("input = %#v", input)
	}
}

func TestResponsesRequestStateUsesFullInputForChatGPTStatelessMode(t *testing.T) {
	d := &OpenAIDriver{
		providerLabel:     "chatgpt",
		registryName:      "chatgpt/gpt-5.3-codex",
		apiModel:          "gpt-5.3-codex",
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

func TestTrimStatelessConversationPreservesLatestUserRequest(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleSystem, Content: "sys"}}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: "add /new session option"})
	for i := 0; i < 20; i++ {
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("tool chatter ", 20)})
	}

	trimmed := trimStatelessConversation(msgs)

	foundRequest := false
	latestUser := ""
	for _, msg := range trimmed {
		if msg.Role != llm.RoleUser {
			continue
		}
		latestUser = msg.Content
		if msg.Content == "add /new session option" {
			foundRequest = true
		}
	}
	if !foundRequest {
		t.Fatalf("trimmed messages lost latest real user request: %#v", trimmed)
	}
	if latestUser != "add /new session option" {
		t.Fatalf("latest user message = %q, want active request", latestUser)
	}
}

func TestResponseParamsUseStatelessCodexDefaultsForChatGPT(t *testing.T) {
	d := &OpenAIDriver{
		providerLabel:     "chatgpt",
		registryName:      "chatgpt/gpt-5.3-codex",
		apiModel:          "gpt-5.3-codex",
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

func TestToOpenAIMessagesHandlesRoleTool(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "check the repo"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{
			{ID: "c1", Name: "git_status", ArgsJSON: `{}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "M internal/foo.go"},
	}
	out := toOpenAIMessages(msgs, false)
	if len(out) != 3 {
		t.Fatalf("want 3 messages, got %d", len(out))
	}
	for i, m := range out {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("message[%d] failed to marshal: %v", i, err)
		}
		if len(b) == 0 {
			t.Fatalf("message[%d] marshaled to empty", i)
		}
	}
}

func TestOpenCodeGoDeepSeekChatCompletionsDowngradesImageParts(t *testing.T) {
	img := t.TempDir() + "/image.png"
	if err := os.WriteFile(img, []byte("not-a-real-png"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := NewCustomCompatProvider("opencode-go", "sk-test", "https://opencode.ai/zen/go/v1", "opencode-go/deepseek-v4-pro", "deepseek-v4-pro", false, nil)
	params := d.chatCompletionParams([]llm.Message{
		{
			Role:    llm.RoleUser,
			Content: "inspect this screenshot",
			ContentParts: []llm.MessageContentPart{
				{Type: "image", Image: &llm.ImageContent{Path: img, MIMEType: "image/png"}},
			},
		},
	})
	raw, err := json.Marshal(params.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "image_url") {
		t.Fatalf("DeepSeek chat request contains unsupported image_url part: %s", string(raw))
	}
	if !strings.Contains(string(raw), "image attachment omitted") {
		t.Fatalf("DeepSeek chat request missing image omission note: %s", string(raw))
	}
}

func TestToolDefsToOpenAIShape(t *testing.T) {
	defs := []llm.ToolDef{
		{
			Name:        "read_file",
			Description: "Read a file",
			Parameters: []llm.ToolParam{
				{Name: "path", Type: "string", Description: "file path", Required: true},
				{Name: "start_line", Type: "integer", Description: "start line", Required: false},
			},
		},
	}
	tools := toolDefsToOpenAI(defs)
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	b, err := json.Marshal(tools[0])
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "read_file") {
		t.Fatalf("marshaled tool missing name: %s", s)
	}
	if !strings.Contains(s, "path") {
		t.Fatalf("marshaled tool missing path param: %s", s)
	}
}

func TestStreamWithToolsResponsesSendsToolsAndEmitsToolCalls(t *testing.T) {
	t.Parallel()

	type requestTool struct {
		Type        string `json:"type"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Strict      *bool  `json:"strict"`
		Parameters  struct {
			Type       string                    `json:"type"`
			Required   []string                  `json:"required"`
			Properties map[string]map[string]any `json:"properties"`
		} `json:"parameters"`
	}
	type requestBody struct {
		Model      string          `json:"model"`
		ToolChoice json.RawMessage `json:"tool_choice"`
		Tools      []requestTool   `json:"tools"`
	}

	var body requestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/responses"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"id\":\"resp_test\",\"object\":\"response\",\"created_at\":1,\"model\":\"gpt-5.4\",\"output\":[{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_list_dir\",\"name\":\"list_dir\",\"arguments\":\"{\\\"path\\\":\\\".\\\"}\"}],\"usage\":{\"input_tokens\":12,\"output_tokens\":3}}}\n\n"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("mycorp", "sk-test", srv.URL, "mycorp/gpt-5.4", "gpt-5.4", true, nil)
	tools := []llm.ToolDef{
		{
			Name:        "list_dir",
			Description: "List a directory",
			Parameters: []llm.ToolParam{
				{Name: "path", Type: "string", Description: "directory path", Required: true},
			},
		},
	}
	out := make(chan llm.Token, 8)
	if err := d.StreamWithToolsOptions(
		context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "inspect the repo"}},
		tools,
		llm.NativeToolOptions{RequireToolCall: true},
		out,
	); err != nil {
		t.Fatalf("StreamWithToolsOptions() error = %v", err)
	}

	var toks []llm.Token
	for tok := range out {
		toks = append(toks, tok)
	}

	if got, want := body.Model, "gpt-5.4"; got != want {
		t.Fatalf("model = %q, want %q", got, want)
	}
	if got, want := strings.TrimSpace(string(body.ToolChoice)), "\"required\""; got != want {
		t.Fatalf("tool_choice = %q, want %q", got, want)
	}
	if len(body.Tools) != 1 {
		t.Fatalf("tools = %#v, want 1 tool", body.Tools)
	}
	if got, want := body.Tools[0].Type, "function"; got != want {
		t.Fatalf("tool type = %q, want %q", got, want)
	}
	if body.Tools[0].Strict == nil || !*body.Tools[0].Strict {
		t.Fatalf("strict = %#v, want true", body.Tools[0].Strict)
	}
	if got, want := body.Tools[0].Name, "list_dir"; got != want {
		t.Fatalf("tool name = %q, want %q", got, want)
	}
	if got, want := body.Tools[0].Parameters.Type, "object"; got != want {
		t.Fatalf("tool parameters.type = %q, want %q", got, want)
	}
	if len(body.Tools[0].Parameters.Required) != 1 || body.Tools[0].Parameters.Required[0] != "path" {
		t.Fatalf("tool required = %#v, want [path]", body.Tools[0].Parameters.Required)
	}
	if got := body.Tools[0].Parameters.Properties["path"]["description"]; got != "directory path" {
		t.Fatalf("tool path description = %#v, want %q", got, "directory path")
	}
	if len(toks) != 1 || toks[0].ToolCall == nil {
		t.Fatalf("tokens = %#v, want one tool call token", toks)
	}
	if got, want := toks[0].ToolCall.ID, "call_list_dir"; got != want {
		t.Fatalf("tool call id = %q, want %q", got, want)
	}
	if got, want := toks[0].ToolCall.Name, "list_dir"; got != want {
		t.Fatalf("tool call name = %q, want %q", got, want)
	}
	if got, want := toks[0].ToolCall.ArgsJSON, `{"path":"."}`; got != want {
		t.Fatalf("tool call args = %q, want %q", got, want)
	}
}

func TestToolDefSchemaHonorsRequiredParameters(t *testing.T) {
	schema := toolDefSchema(llm.ToolDef{
		Name: "read_file",
		Parameters: []llm.ToolParam{
			{Name: "path", Type: "string", Required: true},
			{Name: "start_line", Type: "integer", Required: false},
			{Name: "end_line", Type: "integer", Required: false},
		},
	})

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("required = %#v, want []string", schema["required"])
	}
	if len(required) != 1 || required[0] != "path" {
		t.Fatalf("required = %#v, want [path]", required)
	}
}

func TestToolDefsToResponsesStrictSchemaRequiresOptionalNullableParams(t *testing.T) {
	defs := []llm.ToolDef{{
		Name: "spawn_agent",
		Parameters: []llm.ToolParam{
			{Name: "task_description", Type: "string", Required: true},
			{Name: "role", Type: "string", Required: false},
			{Name: "work_dir", Type: "string", Required: false},
		},
	}}
	tools := toolDefsToResponses(defs)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one", tools)
	}
	b, err := json.Marshal(tools[0])
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	params := got["parameters"].(map[string]any)
	required := stringSliceFromAny(params["required"])
	for _, want := range []string{"task_description", "role", "work_dir"} {
		if !stringSliceContains(required, want) {
			t.Fatalf("required = %#v, want %s", required, want)
		}
	}
	props := params["properties"].(map[string]any)
	for _, optional := range []string{"role", "work_dir"} {
		prop := props[optional].(map[string]any)
		if !typeAllowsNull(prop["type"]) {
			t.Fatalf("%s type = %#v, want nullable", optional, prop["type"])
		}
	}
}

func TestToolDefsToResponsesStrictSchemaHandlesEmptyObjectsAndNullableEnums(t *testing.T) {
	defs := []llm.ToolDef{{
		Name: "audit_repo",
		Schema: &llm.ToolSchema{
			Type:     "object",
			Required: []string{"path"},
			Properties: map[string]*llm.ToolSchema{
				"path":    {Type: "string"},
				"mode":    {Type: "string", Enum: []string{"quick", "deep"}},
				"options": {Type: "object"},
			},
		},
	}}
	tools := toolDefsToResponses(defs)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one", tools)
	}
	b, err := json.Marshal(tools[0])
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	params := got["parameters"].(map[string]any)
	if params["additionalProperties"] != false {
		t.Fatalf("root additionalProperties = %#v, want false", params["additionalProperties"])
	}
	props := params["properties"].(map[string]any)
	options := props["options"].(map[string]any)
	if !typeAllowsNull(options["type"]) {
		t.Fatalf("options type = %#v, want nullable", options["type"])
	}
	if options["additionalProperties"] != false {
		t.Fatalf("options additionalProperties = %#v, want false", options["additionalProperties"])
	}
	mode := props["mode"].(map[string]any)
	if !typeAllowsNull(mode["type"]) {
		t.Fatalf("mode type = %#v, want nullable", mode["type"])
	}
	if !anySliceContainsNull(mode["enum"]) {
		t.Fatalf("mode enum = %#v, want nullable enum", mode["enum"])
	}
}

func stringSliceFromAny(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func typeAllowsNull(v any) bool {
	raw, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range raw {
		if item == "null" {
			return true
		}
	}
	return false
}

func anySliceContainsNull(v any) bool {
	raw, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range raw {
		if item == nil {
			return true
		}
	}
	return false
}

func TestToolDefSchemaUsesStructuredSchema(t *testing.T) {
	additional := false
	schema := toolDefSchema(llm.ToolDef{
		Name: "update_plan",
		Schema: &llm.ToolSchema{
			Type: "object",
			Properties: map[string]*llm.ToolSchema{
				"steps": {
					Type: "array",
					Items: &llm.ToolSchema{
						Type: "object",
						Properties: map[string]*llm.ToolSchema{
							"step":   {Type: "string"},
							"status": {Type: "string", Enum: []string{"pending", "in_progress", "blocked", "completed"}},
						},
						Required:             []string{"step", "status"},
						AdditionalProperties: &additional,
					},
				},
			},
			Required:             []string{"steps"},
			AdditionalProperties: &additional,
		},
	})

	props := schema["properties"].(map[string]any)
	steps := props["steps"].(map[string]any)
	if steps["type"] != "array" {
		t.Fatalf("steps.type = %#v, want array", steps["type"])
	}
	item := steps["items"].(map[string]any)
	status := item["properties"].(map[string]any)["status"].(map[string]any)
	if got := status["enum"].([]string); len(got) != 4 || got[1] != "in_progress" {
		t.Fatalf("status enum = %#v", got)
	}
}

func TestCustomCompatProviderFallsBackFromResponsesToolsToChatCompletions(t *testing.T) {
	t.Parallel()

	type chatRequestBody struct {
		Model      string            `json:"model"`
		ToolChoice json.RawMessage   `json:"tool_choice"`
		Tools      []json.RawMessage `json:"tools"`
	}

	var paths []string
	var chatBody chatRequestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/responses":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"responses tools unsupported"}}`))
		case "/chat/completions":
			if err := json.NewDecoder(r.Body).Decode(&chatBody); err != nil {
				t.Fatalf("decode chat body: %v", err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_list_dir\",\"type\":\"function\",\"function\":{\"name\":\"list_dir\",\"arguments\":\"{\\\"path\\\":\\\".\\\"}\"}}]},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("oca", "sk-test", srv.URL, "oca/gpt-5.4", "gpt-5.4", true, nil)
	tools := []llm.ToolDef{
		{
			Name:        "list_dir",
			Description: "List a directory",
			Parameters: []llm.ToolParam{
				{Name: "path", Type: "string", Description: "directory path", Required: true},
			},
		},
	}
	out := make(chan llm.Token, 8)
	if err := d.StreamWithToolsOptions(
		context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "inspect the repo"}},
		tools,
		llm.NativeToolOptions{RequireToolCall: true},
		out,
	); err != nil {
		t.Fatalf("StreamWithToolsOptions() error = %v", err)
	}

	var toks []llm.Token
	for tok := range out {
		toks = append(toks, tok)
	}

	if len(paths) != 2 || paths[0] != "/responses" || paths[1] != "/chat/completions" {
		t.Fatalf("paths = %#v, want [/responses /chat/completions]", paths)
	}
	if got, want := chatBody.Model, "gpt-5.4"; got != want {
		t.Fatalf("chat model = %q, want %q", got, want)
	}
	if got, want := strings.TrimSpace(string(chatBody.ToolChoice)), "\"required\""; got != want {
		t.Fatalf("chat tool_choice = %q, want %q", got, want)
	}
	if len(chatBody.Tools) != 1 {
		t.Fatalf("chat tools = %d, want 1", len(chatBody.Tools))
	}
	if len(toks) != 1 || toks[0].ToolCall == nil {
		t.Fatalf("tokens = %#v, want one tool call token", toks)
	}
	if got, want := toks[0].ToolCall.ID, "call_list_dir"; got != want {
		t.Fatalf("tool call id = %q, want %q", got, want)
	}
	if got, want := toks[0].ToolCall.Name, "list_dir"; got != want {
		t.Fatalf("tool call name = %q, want %q", got, want)
	}
}

func TestChatCompletionsToolCallIDKeepsFirstRepeatedStreamValue(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/chat/completions"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_write\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":\"{\\\"path\\\":\\\"\"}}]},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_write\",\"type\":\"function\",\"function\":{\"arguments\":\"README.md\\\",\\\"content\\\":\\\"hi\\\"}\"}}]},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/kimi-k2.6", "kimi-k2.6", false, nil)
	tools := []llm.ToolDef{{Name: "write_file", Description: "Write a file"}}
	out := make(chan llm.Token, 8)
	if err := d.StreamWithToolsOptions(
		context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "write file"}},
		tools,
		llm.NativeToolOptions{},
		out,
	); err != nil {
		t.Fatalf("StreamWithToolsOptions() error = %v", err)
	}

	var toks []llm.Token
	for tok := range out {
		toks = append(toks, tok)
	}

	if len(toks) != 1 || toks[0].ToolCall == nil {
		t.Fatalf("tokens = %#v, want one tool call token", toks)
	}
	if got, want := toks[0].ToolCall.ID, "call_write"; got != want {
		t.Fatalf("tool call id = %q, want %q", got, want)
	}
	if got, want := toks[0].ToolCall.ArgsJSON, `{"path":"README.md","content":"hi"}`; got != want {
		t.Fatalf("tool call args = %q, want %q", got, want)
	}
}

func TestOpenCodeGoDeepSeekReasonerOmitsRequiredToolChoice(t *testing.T) {
	t.Parallel()

	type chatRequestBody struct {
		Model      string            `json:"model"`
		ToolChoice json.RawMessage   `json:"tool_choice"`
		Tools      []json.RawMessage `json:"tools"`
	}

	var chatBody chatRequestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/chat/completions"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if err := json.NewDecoder(r.Body).Decode(&chatBody); err != nil {
			t.Fatalf("decode chat body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/deepseek-v4-pro", "deepseek-reasoner", false, nil)
	tools := []llm.ToolDef{{
		Name:        "spawn_agent",
		Description: "Spawn child agent",
		Parameters: []llm.ToolParam{
			{Name: "role", Type: "string", Description: "agent role", Required: true},
		},
	}}
	out := make(chan llm.Token, 8)
	if err := d.StreamWithToolsOptions(
		context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "delegate this task"}},
		tools,
		llm.NativeToolOptions{RequireToolCall: true},
		out,
	); err != nil {
		t.Fatalf("StreamWithToolsOptions() error = %v", err)
	}
	for range out {
	}

	if got, want := chatBody.Model, "deepseek-reasoner"; got != want {
		t.Fatalf("model = %q, want %q", got, want)
	}
	if len(chatBody.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(chatBody.Tools))
	}
	if len(chatBody.ToolChoice) != 0 {
		t.Fatalf("tool_choice = %s, want omitted for opencode-go deepseek reasoner", string(chatBody.ToolChoice))
	}
}

func TestOpenCodeGoKimiThinkingOmitsRequiredToolChoice(t *testing.T) {
	t.Parallel()

	type chatRequestBody struct {
		Model      string            `json:"model"`
		ToolChoice json.RawMessage   `json:"tool_choice"`
		Tools      []json.RawMessage `json:"tools"`
	}

	var chatBody chatRequestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/chat/completions"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if err := json.NewDecoder(r.Body).Decode(&chatBody); err != nil {
			t.Fatalf("decode chat body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/kimi-k2.6", "kimi-k2.6", false, nil)
	tools := []llm.ToolDef{{
		Name:        "spawn_agent",
		Description: "Spawn child agent",
		Parameters: []llm.ToolParam{
			{Name: "role", Type: "string", Description: "agent role", Required: true},
		},
	}}
	out := make(chan llm.Token, 8)
	if err := d.StreamWithToolsOptions(
		context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "delegate this task"}},
		tools,
		llm.NativeToolOptions{RequireToolCall: true},
		out,
	); err != nil {
		t.Fatalf("StreamWithToolsOptions() error = %v", err)
	}
	for range out {
	}

	if got, want := chatBody.Model, "kimi-k2.6"; got != want {
		t.Fatalf("model = %q, want %q", got, want)
	}
	if len(chatBody.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(chatBody.Tools))
	}
	if len(chatBody.ToolChoice) != 0 {
		t.Fatalf("tool_choice = %s, want omitted for opencode-go kimi thinking model", string(chatBody.ToolChoice))
	}
}

func TestOpenCodeGoKimiThinkingReplaysReasoningContentOnAssistantToolCalls(t *testing.T) {
	t.Parallel()

	type chatRequestBody struct {
		Messages []map[string]any `json:"messages"`
	}

	var chatBody chatRequestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/chat/completions"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if err := json.NewDecoder(r.Body).Decode(&chatBody); err != nil {
			t.Fatalf("decode chat body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/kimi-k2.6", "kimi-k2.6", false, nil)
	tools := []llm.ToolDef{{Name: "wait_agent", Description: "Wait for child agent"}}
	out := make(chan llm.Token, 8)
	if err := d.StreamWithToolsOptions(
		context.Background(),
		[]llm.Message{
			{Role: llm.RoleUser, Content: "delegate this task"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "call_wait", Name: "wait_agent", ArgsJSON: `{"id":"agent-1"}`}}},
			{Role: llm.RoleTool, ToolCallID: "call_wait", Content: `{"status":"completed"}`},
			{Role: llm.RoleUser, Content: "continue"},
		},
		tools,
		llm.NativeToolOptions{},
		out,
	); err != nil {
		t.Fatalf("StreamWithToolsOptions() error = %v", err)
	}
	for range out {
	}

	for _, msg := range chatBody.Messages {
		if msg["role"] != "assistant" || msg["tool_calls"] == nil {
			continue
		}
		reasoning, ok := msg["reasoning_content"].(string)
		if !ok {
			t.Fatalf("assistant tool-call message missing reasoning_content: %#v", msg)
		}
		if strings.TrimSpace(reasoning) == "" {
			t.Fatalf("assistant tool-call message reasoning_content is empty: %#v", msg)
		}
		return
	}
	t.Fatalf("request missing assistant tool-call message: %#v", chatBody.Messages)
}

func TestOpenCodeGoSupportedChatModelsUseReasoningToolReplayCompatibility(t *testing.T) {
	t.Parallel()

	type chatRequestBody struct {
		Model      string           `json:"model"`
		Messages   []map[string]any `json:"messages"`
		ToolChoice json.RawMessage  `json:"tool_choice"`
	}

	for _, model := range []string{
		"glm-5.1",
		"glm-5",
		"kimi-k2.6",
		"kimi-k2.5",
		"deepseek-v4-pro",
		"deepseek-v4-flash",
		"mimo-v2.5-pro",
		"mimo-v2.5",
		"mimo-v2-pro",
		"mimo-v2-omni",
	} {
		model := model
		t.Run(model, func(t *testing.T) {
			var chatBody chatRequestBody
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got, want := r.URL.Path, "/chat/completions"; got != want {
					t.Fatalf("path = %q, want %q", got, want)
				}
				if err := json.NewDecoder(r.Body).Decode(&chatBody); err != nil {
					t.Fatalf("decode chat body: %v", err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"))
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
			}))
			defer srv.Close()

			d := NewCustomCompatProvider("opencode-go", "sk-test", srv.URL, "opencode-go/"+model, model, false, nil)
			tools := []llm.ToolDef{{Name: "wait_agent", Description: "Wait for child agent"}}
			out := make(chan llm.Token, 8)
			if err := d.StreamWithToolsOptions(
				context.Background(),
				[]llm.Message{
					{Role: llm.RoleUser, Content: "delegate this task"},
					{Role: llm.RoleAssistant, Content: "I will inspect this first."},
					{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "call_wait", Name: "wait_agent", ArgsJSON: `{"id":"agent-1"}`}}},
					{Role: llm.RoleTool, ToolCallID: "call_wait", Content: `{"status":"completed"}`},
					{Role: llm.RoleUser, Content: "continue"},
				},
				tools,
				llm.NativeToolOptions{RequireToolCall: true},
				out,
			); err != nil {
				t.Fatalf("StreamWithToolsOptions() error = %v", err)
			}
			for range out {
			}

			if got := chatBody.Model; got != model {
				t.Fatalf("model = %q, want %q", got, model)
			}
			if len(chatBody.ToolChoice) != 0 {
				t.Fatalf("tool_choice = %s, want omitted for opencode-go %s", string(chatBody.ToolChoice), model)
			}
			assistantMessages := 0
			assistantToolMessages := 0
			for _, msg := range chatBody.Messages {
				if msg["role"] != "assistant" {
					continue
				}
				assistantMessages++
				reasoning, ok := msg["reasoning_content"].(string)
				if !ok {
					t.Fatalf("assistant message missing reasoning_content for %s: %#v", model, msg)
				}
				if strings.TrimSpace(reasoning) == "" {
					t.Fatalf("assistant message reasoning_content is empty for %s: %#v", model, msg)
				}
				if msg["tool_calls"] != nil {
					assistantToolMessages++
				}
			}
			if assistantMessages != 2 || assistantToolMessages != 1 {
				t.Fatalf("assistant messages = %d, tool-call assistant messages = %d for %s: %#v", assistantMessages, assistantToolMessages, model, chatBody.Messages)
			}
		})
	}
}

func TestAssistantReplayReasoningContentPreservesCapturedReasoning(t *testing.T) {
	t.Parallel()

	got := assistantReplayReasoningContent(llm.Message{ReasoningContent: "actual reasoning"}, true)
	if got != "actual reasoning" {
		t.Fatalf("reasoning = %q, want captured reasoning", got)
	}
}

func TestRequiredToolChoiceScopeForOpenCodeGoCompatibility(t *testing.T) {
	t.Parallel()

	if providerSupportsRequiredChatToolChoice("other", "other/deepseek-reasoner", "deepseek-reasoner") {
		// Non-opencode providers keep their existing behavior unless a provider-specific
		// incompatibility is proven.
	} else {
		t.Fatal("non-opencode deepseek-reasoner should preserve required tool_choice")
	}
	if !providerSupportsRequiredChatToolChoice("opencode-go", "opencode-go/deepseek-chat", "deepseek-chat") {
		t.Fatal("unreported opencode-go DeepSeek chat model should preserve required tool_choice")
	}
	for _, model := range []string{
		"glm-5.1",
		"glm-5",
		"kimi-k2.6",
		"kimi-k2.5",
		"deepseek-v4-pro",
		"deepseek-v4-flash",
		"mimo-v2.5-pro",
		"mimo-v2.5",
		"mimo-v2-pro",
		"mimo-v2-omni",
	} {
		if providerSupportsRequiredChatToolChoice("opencode-go", "opencode-go/"+model, model) {
			t.Fatalf("opencode-go %s should omit required tool_choice", model)
		}
	}
}

func TestToResponseInputPreservesNativeToolHistory(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "inspect the repo"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{
			{ID: "call_list_dir", Name: "list_dir", ArgsJSON: `{"path":"."}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "call_list_dir", Content: "README.md\ninternal"},
	}

	out := toResponseInput(msgs)
	if len(out) != 3 {
		t.Fatalf("want 3 input items, got %d", len(out))
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"type":"function_call"`) {
		t.Fatalf("expected function_call in %s", s)
	}
	if !strings.Contains(s, `"call_id":"call_list_dir"`) {
		t.Fatalf("expected call_id in %s", s)
	}
	if !strings.Contains(s, `"name":"list_dir"`) {
		t.Fatalf("expected function name in %s", s)
	}
	if !strings.Contains(s, `"type":"function_call_output"`) {
		t.Fatalf("expected function_call_output in %s", s)
	}
	var items []map[string]any
	if err := json.Unmarshal(b, &items); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if got, ok := items[2]["output"].(string); !ok || got != "README.md\ninternal" {
		t.Fatalf("tool output = %#v, want %q", items[2]["output"], "README.md\ninternal")
	}
}

func TestRepairToolCallArgsJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantJSON bool
	}{
		{"valid JSON", `{"command":"ls"}`, true},
		{"missing closer", `{"command":"ls"`, true},
		{"empty", ``, true},
		{"garbage", `not json at all`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := repairToolCallArgsJSON(tt.input)
			if tt.wantJSON {
				if !json.Valid([]byte(got)) {
					t.Fatalf("expected valid JSON, got %q", got)
				}
			} else {
				if got != tt.input {
					t.Fatalf("expected unrepaired garbage to be preserved, got %q", got)
				}
			}
		})
	}
}

func assertErr(msg string) error { return simpleErr(msg) }

type simpleErr string

func (e simpleErr) Error() string { return string(e) }
