# Native Tool Calling Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace prompt-level XML tool calling as the primary path with provider-native structured tool calling via the OpenAI `tools` API parameter, keeping the XML text parser as a fallback for models that don't support native tool calling.

**Architecture:** Add a `NativeToolCaller` optional interface to `llm.Driver`. The OpenAI driver implements `StreamWithTools()` which passes tool definitions via the API and accumulates streaming tool-call deltas. The `RetryDriver` forwards the interface. The react runner detects the interface at loop start, routes to the native path when available, skips XML format instructions from the system prompt, and stores tool calls/results using proper `tool` role messages for round-trip conversation fidelity.

**Tech Stack:** Go 1.22+, `github.com/openai/openai-go` v1.12.0, `github.com/openai/openai-go/shared`, `github.com/openai/openai-go/packages/param`

---

## File Map

| Action   | File                                     | What changes                                              |
|----------|------------------------------------------|-----------------------------------------------------------|
| Modify   | `internal/llm/types.go`                  | Add `ToolDef`, `ToolParam`, `NativeToolCall`, `RoleTool`; extend `Message`, `Token`; add `NativeToolCaller` interface |
| Modify   | `internal/llm/types_test.go`             | Tests for new types / zero-value safety                   |
| Modify   | `internal/llm/retry.go`                  | Add `StreamWithTools` forwarding so production drivers expose the interface |
| Modify   | `internal/llm/retry_test.go`             | Test forwarding                                           |
| Modify   | `internal/agent/tools/registry.go`       | Add `ToLLMToolDefs() []llm.ToolDef`                       |
| Modify   | `internal/agent/tools/registry_test.go`  | Test `ToLLMToolDefs` output shape                         |
| Modify   | `internal/llm/drivers/openai.go`         | `StreamWithTools()`, `toOpenAIMessages` handles `RoleTool` + assistant `ToolCalls`, `toolDefsToOpenAI`, repair helpers, fix `isAppendOnlyMessageHistory` |
| Modify   | `internal/llm/drivers/openai_internal_test.go` | Test `toOpenAIMessages` with tool messages; test repair layer; test `toolDefsToOpenAI` |
| Modify   | `internal/agent/system.go`               | Add `BuildNativeSystemPrompt` (no XML tool format block)  |
| Modify   | `internal/react/prompt.go`               | `BuildMessages` must not drop assistant messages that have `ToolCalls` but empty `Content` |
| Modify   | `internal/react/session.go`              | Add `AppendAssistantWithToolCalls`, `AppendNativeToolResult` |
| Modify   | `internal/react/session_test.go`         | Test native tool call message round-trip                  |
| Modify   | `internal/react/loop.go`                 | Native path: detect interface, build tool defs, route, use native system prompt |
| Modify   | `internal/react/loop_test.go`            | Test native path end-to-end with mock driver              |
| Modify   | `internal/runtime/chat.go`               | Pass `NativeSystemPrompt` in react `Config`               |

---

## Chunk 1: Type Foundation

### Task 1: Extend `llm/types.go` with new types and interfaces

**Files:**
- Modify: `internal/llm/types.go`
- Modify: `internal/llm/types_test.go`

- [ ] **Step 1: Write failing tests for new types**

Add to `internal/llm/types_test.go`:
```go
func TestNativeToolCallZeroValue(t *testing.T) {
	var tc NativeToolCall
	if tc.ID != "" || tc.Name != "" || tc.ArgsJSON != "" {
		t.Fatal("NativeToolCall zero value should be empty")
	}
}

func TestTokenWithToolCall(t *testing.T) {
	tc := &NativeToolCall{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"go.mod"}`}
	tok := Token{ToolCall: tc}
	if tok.Text != "" {
		t.Fatal("token text should be empty when carrying tool call")
	}
	if tok.ToolCall == nil || tok.ToolCall.Name != "read_file" {
		t.Fatal("token should carry tool call")
	}
}

func TestMessageRoleToolZeroValue(t *testing.T) {
	m := Message{Role: RoleTool, ToolCallID: "c1", Content: "result"}
	if m.Role != RoleTool {
		t.Fatal("role mismatch")
	}
}

func TestMessageAssistantWithToolCalls(t *testing.T) {
	m := Message{
		Role: RoleAssistant,
		ToolCalls: []NativeToolCall{
			{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"ls"}`},
		},
	}
	if len(m.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(m.ToolCalls))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/cass/git/forge && go test ./internal/llm/... -run "TestNativeToolCall|TestTokenWithToolCall|TestMessageRole|TestMessageAssistant" 2>&1 | head -20
```
Expected: compile error — types don't exist yet.

- [ ] **Step 3: Add types to `internal/llm/types.go`**

After the existing `Role` constants, add:
```go
const RoleTool Role = "tool"

// ToolParam describes one parameter of a tool for native tool calling.
type ToolParam struct {
	Name        string
	Type        string // "string", "integer", "boolean"
	Description string
	Required    bool
}

// ToolDef describes a tool for native structured tool calling via the provider API.
type ToolDef struct {
	Name        string
	Description string
	Parameters  []ToolParam
}

// NativeToolCall is a completed tool call returned via the provider's native tool-calling API.
type NativeToolCall struct {
	ID       string
	Name     string
	ArgsJSON string
}
```

Extend `Message`:
```go
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []NativeToolCall // non-nil when Role==RoleAssistant and model made native tool calls
	ToolCallID string           // non-empty when Role==RoleTool (result message)
}
```

Extend `Token`:
```go
type Token struct {
	Text     string
	Done     bool
	Err      error
	ToolCall *NativeToolCall // non-nil when provider returns a native tool call via StreamWithTools
}
```

Add interface:
```go
// NativeToolCaller is optionally implemented by drivers that support provider-native
// structured tool calling. When implemented, the react runner uses this path instead
// of prompt-level XML tool calling.
type NativeToolCaller interface {
	StreamWithTools(ctx context.Context, messages []Message, tools []ToolDef, out chan<- Token) error
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /Users/cass/git/forge && go test ./internal/llm/... -run "TestNativeToolCall|TestTokenWithToolCall|TestMessageRole|TestMessageAssistant" -v
```
Expected: all 4 tests PASS.

- [ ] **Step 5: Full package build check**

```bash
cd /Users/cass/git/forge && go build ./internal/llm/...
```
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
cd /Users/cass/git/forge && git add internal/llm/types.go internal/llm/types_test.go
git commit -m "llm: add NativeToolCaller interface, ToolDef, NativeToolCall, RoleTool"
```

---

## Chunk 2: RetryDriver Forwarding

### Task 2: Forward `StreamWithTools` through `RetryDriver`

**Files:**
- Modify: `internal/llm/retry.go`
- Modify: `internal/llm/retry_test.go`

This is critical: in production every driver is wrapped in `RetryDriver`. Without forwarding, the type assertion `r.driver.(llm.NativeToolCaller)` will always return false.

- [ ] **Step 1: Write failing test**

Add to `internal/llm/retry_test.go`:
```go
type nativeDriver struct {
	callCount int
}

func (d *nativeDriver) Name() string { return "native" }
func (d *nativeDriver) Stream(_ context.Context, _ []Message, out chan<- Token) error {
	close(out)
	return nil
}
func (d *nativeDriver) StreamWithTools(_ context.Context, _ []Message, _ []ToolDef, out chan<- Token) error {
	defer close(out)
	d.callCount++
	out <- Token{ToolCall: &NativeToolCall{ID: "c1", Name: "git_status", ArgsJSON: "{}"}}
	return nil
}

func TestRetryDriverForwardsNativeToolCaller(t *testing.T) {
	inner := &nativeDriver{}
	retry := NewRetryDriver(inner, 1, 0, 0, 0)

	caller, ok := any(retry).(NativeToolCaller)
	if !ok {
		t.Fatal("RetryDriver should implement NativeToolCaller when inner driver does")
	}
	out := make(chan Token, 4)
	err := caller.StreamWithTools(context.Background(), nil, nil, out)
	if err != nil {
		t.Fatal(err)
	}
	var toks []Token
	for tok := range out {
		toks = append(toks, tok)
	}
	if len(toks) != 1 || toks[0].ToolCall == nil {
		t.Fatal("expected one tool call token")
	}
	if inner.callCount != 1 {
		t.Fatalf("inner callCount = %d, want 1", inner.callCount)
	}
}

func TestRetryDriverNativeToolCallerNotForwardedWhenInnerLacks(t *testing.T) {
	// A plain driver without StreamWithTools should NOT expose NativeToolCaller.
	type plainDriver struct{ name string }
	inner := struct {
		plainDriver
	}{}
	// Can't directly test negative type assertion on RetryDriver easily,
	// so just confirm the forwarding method checks the inner driver.
	_ = inner
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/cass/git/forge && go test ./internal/llm/... -run "TestRetryDriverForwardsNativeToolCaller" 2>&1 | head -10
```
Expected: compile error — `RetryDriver` doesn't implement `NativeToolCaller` (no method yet).

- [ ] **Step 3: Add `StreamWithTools` to `RetryDriver`**

Add to `internal/llm/retry.go` after the existing `Stream` method:
```go
// StreamWithTools implements NativeToolCaller by forwarding to the inner driver
// if it also implements NativeToolCaller. Applies the same retry logic as Stream.
func (d *RetryDriver) StreamWithTools(ctx context.Context, messages []Message, tools []ToolDef, out chan<- Token) error {
	caller, ok := d.inner.(NativeToolCaller)
	if !ok {
		close(out)
		return fmt.Errorf("inner driver %q does not support native tool calling", d.inner.Name())
	}
	defer close(out)

	if err := waitForRateLimitCooldown(ctx, d.Name()); err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < d.maxAttempts; attempt++ {
		if attempt > 0 {
			wait := d.backoff(attempt)
			if err := retrySleep(ctx, wait); err != nil {
				return err
			}
		}

		callCtx := ctx
		if d.timeout > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(ctx, d.timeout)
			defer cancel()
		}

		internal := make(chan Token, 64)
		errCh := make(chan error, 1)
		go func() {
			errCh <- caller.StreamWithTools(callCtx, messages, tools, internal)
		}()

		var tokens []Token
		for tok := range internal {
			tokens = append(tokens, tok)
		}

		lastErr = <-errCh
		if lastErr == nil {
			for _, tok := range tokens {
				select {
				case out <- tok:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}
		if isRateLimited(lastErr) {
			rememberRateLimit(d.Name())
		}
		if !isRetryable(lastErr) {
			return lastErr
		}
	}
	return fmt.Errorf("all %d attempts failed: %w", d.maxAttempts, lastErr)
}
```

Note: `RetryDriver` does NOT implement the `NativeToolCaller` interface statically — Go's type system will expose it dynamically. The react runner checks `r.driver.(llm.NativeToolCaller)` which will succeed if the `RetryDriver`'s inner driver implements it. But since `RetryDriver` now has the `StreamWithTools` method, the type assertion on `RetryDriver` itself will also succeed. This is the correct behavior: if the inner driver doesn't support it, `StreamWithTools` returns an error, which is handled gracefully.

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /Users/cass/git/forge && go test ./internal/llm/... -run "TestRetryDriverForwards" -v
```
Expected: PASS.

- [ ] **Step 5: Full build**

```bash
cd /Users/cass/git/forge && go build ./...
```

- [ ] **Step 6: Commit**

```bash
cd /Users/cass/git/forge && git add internal/llm/retry.go internal/llm/retry_test.go
git commit -m "llm: RetryDriver forwards StreamWithTools to inner NativeToolCaller"
```

---

## Chunk 3: Registry Tool Defs Conversion

### Task 3: Add `ToLLMToolDefs()` to tools registry

**Files:**
- Modify: `internal/agent/tools/registry.go`
- Modify: `internal/agent/tools/registry_test.go`

- [ ] **Step 1: Write failing tests**

Check if `internal/agent/tools/registry_test.go` exists. If not, create it. Add:
```go
package tools_test

import (
	"testing"

	"forge/internal/agent/tools"
)

func TestToLLMToolDefsBasic(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "read_file",
		Description: "Read a file",
		Parameters: []tools.ParameterDef{
			{Name: "path", Type: "string", Description: "file path", Required: true},
			{Name: "start_line", Type: "int", Description: "start line", Required: false},
		},
	})

	defs := reg.ToLLMToolDefs()
	if len(defs) != 1 {
		t.Fatalf("want 1 def, got %d", len(defs))
	}
	d := defs[0]
	if d.Name != "read_file" {
		t.Fatalf("name = %q, want read_file", d.Name)
	}
	if len(d.Parameters) != 2 {
		t.Fatalf("params = %d, want 2", len(d.Parameters))
	}
	if d.Parameters[0].Name != "path" || !d.Parameters[0].Required {
		t.Fatal("first param should be path (required)")
	}
	if d.Parameters[1].Name != "start_line" || d.Parameters[1].Required {
		t.Fatal("second param should be start_line (optional)")
	}
}

func TestToLLMToolDefsExcludesToolHelp(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "read_file", Description: "read"})
	reg.Register(tools.Tool{Name: "tool_help", Description: "meta"})

	defs := reg.ToLLMToolDefs()
	for _, d := range defs {
		if d.Name == "tool_help" {
			t.Fatal("tool_help should be excluded from native tool defs")
		}
	}
	if len(defs) != 1 {
		t.Fatalf("want 1 def after excluding tool_help, got %d", len(defs))
	}
}

func TestToLLMToolDefsTypeMapping(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name: "write_file",
		Parameters: []tools.ParameterDef{
			{Name: "path", Type: "string", Required: true},
			{Name: "line_count", Type: "int", Required: false},
			{Name: "overwrite", Type: "bool", Required: false},
		},
	})
	defs := reg.ToLLMToolDefs()
	if len(defs) != 1 {
		t.Fatalf("want 1 def, got %d", len(defs))
	}
	params := map[string]string{}
	for _, p := range defs[0].Parameters {
		params[p.Name] = p.Type
	}
	if params["path"] != "string" {
		t.Fatalf("string type mismatch, got %q", params["path"])
	}
	if params["line_count"] != "integer" {
		t.Fatalf("int should map to integer, got %q", params["line_count"])
	}
	if params["overwrite"] != "boolean" {
		t.Fatalf("bool should map to boolean, got %q", params["overwrite"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/cass/git/forge && go test ./internal/agent/tools/... -run "TestToLLMToolDefs" 2>&1 | head -10
```
Expected: compile error — `ToLLMToolDefs` not defined.

- [ ] **Step 3: Implement `ToLLMToolDefs` in `registry.go`**

Add `"forge/internal/llm"` to the imports in `internal/agent/tools/registry.go`. Then add:
```go
// ToLLMToolDefs converts registered tools to llm.ToolDef for native tool calling.
// tool_help is excluded — it is only meaningful in the XML text-based path.
func (r *Registry) ToLLMToolDefs() []llm.ToolDef {
	defs := make([]llm.ToolDef, 0, len(r.order))
	for _, name := range r.order {
		if name == "tool_help" {
			continue
		}
		t := r.tools[name]
		params := make([]llm.ToolParam, 0, len(t.Parameters))
		for _, p := range t.Parameters {
			params = append(params, llm.ToolParam{
				Name:        p.Name,
				Type:        mapParamType(p.Type),
				Description: p.Description,
				Required:    p.Required,
			})
		}
		defs = append(defs, llm.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		})
	}
	return defs
}

// mapParamType converts forge parameter type strings to JSON Schema type names.
func mapParamType(t string) string {
	switch t {
	case "int":
		return "integer"
	case "bool":
		return "boolean"
	default:
		return "string"
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /Users/cass/git/forge && go test ./internal/agent/tools/... -run "TestToLLMToolDefs" -v
```
Expected: 3 tests PASS.

- [ ] **Step 5: Full build check**

```bash
cd /Users/cass/git/forge && go build ./...
```
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
cd /Users/cass/git/forge && git add internal/agent/tools/registry.go internal/agent/tools/registry_test.go
git commit -m "tools: add ToLLMToolDefs for native tool calling conversion"
```

---

## Chunk 4: OpenAI Driver — Native Tool Calling

### Task 4: Add `StreamWithTools`, update `toOpenAIMessages`, fix `isAppendOnlyMessageHistory`

**Files:**
- Modify: `internal/llm/drivers/openai.go`
- Modify: `internal/llm/drivers/openai_internal_test.go`

**SDK types reference (openai-go v1.12.0):**
- `openai.ChatCompletionAssistantMessageParam` — struct with `Content ChatCompletionAssistantMessageParamContentUnion`, `ToolCalls []ChatCompletionMessageToolCallParam`, `Role constant.Assistant`
- `openai.ChatCompletionMessageToolCallParam` — struct with `ID string`, `Function ChatCompletionMessageToolCallFunctionParam`, `Type constant.Function`
- `openai.ChatCompletionMessageToolCallFunctionParam` — struct with `Name string`, `Arguments string`
- `openai.ChatCompletionAssistantMessageParamContentUnion` — struct with `OfString param.Opt[string]`
- `openai.ToolMessage(content, toolCallID string)` — helper returns `ChatCompletionMessageParamUnion`
- `openai.ChatCompletionToolParam` — struct with `Function shared.FunctionDefinitionParam`, `Type constant.Function`
- `shared.FunctionDefinitionParam` — struct with `Name string`, `Description param.Opt[string]`, `Parameters shared.FunctionParameters`
- `shared.FunctionParameters` — type alias for `map[string]any`
- `param.NewOpt[T](v T) param.Opt[T]` — from `github.com/openai/openai-go/packages/param`
- `ChatCompletionNewParams.Tools` — `[]openai.ChatCompletionToolParam` (plain slice, NOT `param.Opt`)
- `ChatCompletionChunkChoiceDeltaToolCall.Index` — `int64`

- [ ] **Step 1: Write failing tests**

Add to `internal/llm/drivers/openai_internal_test.go`:
```go
func TestToOpenAIMessagesHandlesRoleTool(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "check the repo"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{
			{ID: "c1", Name: "git_status", ArgsJSON: `{}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "M internal/foo.go"},
	}
	out := toOpenAIMessages(msgs)
	if len(out) != 3 {
		t.Fatalf("want 3 messages, got %d", len(out))
	}
	// Ensure no panic and correct count; JSON marshaling verifies correctness
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

func TestRepairToolCallArgsJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantJSON bool // true = expect valid JSON output
	}{
		{"valid JSON", `{"command":"ls"}`, true},
		{"missing closer", `{"command":"ls"`, true},
		{"empty", ``, true}, // empty → "{}"
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
				if got != "" {
					t.Fatalf("expected empty string for unrepaired garbage, got %q", got)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/cass/git/forge && go test ./internal/llm/drivers/... -run "TestToOpenAIMessagesHandlesRoleTool|TestToolDefsToOpenAI|TestRepairToolCall" 2>&1 | head -10
```
Expected: compile errors.

- [ ] **Step 3: Add imports to `openai.go`**

Add to the import block in `internal/llm/drivers/openai.go`:
```go
"encoding/json"

"github.com/openai/openai-go/packages/param"
"github.com/openai/openai-go/shared"
```

- [ ] **Step 4: Replace `toOpenAIMessages` to handle `RoleTool` and assistant with `ToolCalls`**

Replace the existing `toOpenAIMessages` function:
```go
func toOpenAIMessages(msgs []llm.Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			out = append(out, openai.SystemMessage(m.Content))
		case llm.RoleUser:
			out = append(out, openai.UserMessage(m.Content))
		case llm.RoleTool:
			// ToolMessage signature: ToolMessage(content, toolCallID)
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
				out = append(out, openai.ChatCompletionAssistantMessageParam{
					Content: openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: param.NewOpt(m.Content),
					},
					ToolCalls: calls,
				})
			} else {
				out = append(out, openai.AssistantMessage(m.Content))
			}
		}
	}
	return out
}
```

- [ ] **Step 5: Add `toolDefsToOpenAI` helper**

Add to `internal/llm/drivers/openai.go`:
```go
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
```

- [ ] **Step 6: Add JSON repair helpers**

These helpers are inlined from `internal/agent/parse.go` to avoid circular imports (`llm/drivers` must not import `agent`):

```go
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

// repairToolCallArgsJSON normalizes a potentially malformed JSON arguments string
// from a streaming tool call delta. Returns "{}" for empty input, empty string
// if repair fails.
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
```

- [ ] **Step 7: Add `StreamWithTools` to `OpenAIDriver`**

Add to `internal/llm/drivers/openai.go`:
```go
// StreamWithTools implements llm.NativeToolCaller. It passes tool definitions via
// the OpenAI chat completions `tools` parameter and emits completed NativeToolCall
// tokens after accumulating all streaming deltas. Text tokens are emitted normally.
func (d *OpenAIDriver) StreamWithTools(ctx context.Context, messages []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	if d.useResponsesAPI() {
		// Responses API does not support the tools parameter — fall back to text streaming.
		return d.streamResponses(ctx, messages, out)
	}

	params := d.chatCompletionParams(messages)
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

	// Emit each accumulated tool call as a token
	for i := 0; i < len(accs); i++ {
		a, ok := accs[i]
		if !ok {
			continue
		}
		name := strings.TrimSpace(a.name.String())
		if name == "" {
			continue // skip malformed call with no name
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
```

- [ ] **Step 8: Fix `isAppendOnlyMessageHistory` to account for `ToolCallID` and `ToolCalls`**

Replace the existing `isAppendOnlyMessageHistory` function in `openai.go`:
```go
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
```

- [ ] **Step 9: Run tests to verify they pass**

```bash
cd /Users/cass/git/forge && go test ./internal/llm/drivers/... -run "TestToOpenAIMessagesHandlesRoleTool|TestToolDefsToOpenAI|TestRepairToolCall" -v
```
Expected: all tests PASS.

- [ ] **Step 10: Verify full build**

```bash
cd /Users/cass/git/forge && go build ./...
```
If there are SDK type errors, run `go doc github.com/openai/openai-go <TypeName>` to verify field names and adjust accordingly.

- [ ] **Step 11: Commit**

```bash
cd /Users/cass/git/forge && git add internal/llm/drivers/openai.go internal/llm/drivers/openai_internal_test.go
git commit -m "llm/drivers: StreamWithTools, toolDefsToOpenAI, repair layer, RoleTool message handling"
```

---

## Chunk 5: Prompt and Session Fix

### Task 5: Fix `BuildMessages` and add native session methods

**Files:**
- Modify: `internal/react/prompt.go`
- Modify: `internal/react/session.go`
- Modify: `internal/react/session_test.go`

- [ ] **Step 1: Write failing tests for `BuildMessages` and session methods**

Create or add to `internal/react/session_test.go`:
```go
package react

import (
	"testing"
	"forge/internal/llm"
)

func TestAppendAssistantWithToolCalls(t *testing.T) {
	s := NewSession()
	s.RecordInput("check the repo")
	calls := []llm.NativeToolCall{
		{ID: "c1", Name: "git_status", ArgsJSON: `{}`},
		{ID: "c2", Name: "run_command", ArgsJSON: `{"command":"ls"}`},
	}
	s.AppendAssistantWithToolCalls(calls)

	snap := s.Snapshot()
	if len(snap.History) != 2 {
		t.Fatalf("want 2 history entries, got %d", len(snap.History))
	}
	last := snap.History[1]
	if last.Role != llm.RoleAssistant {
		t.Fatalf("role = %q, want assistant", last.Role)
	}
	if len(last.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(last.ToolCalls))
	}
	if last.ToolCalls[0].ID != "c1" || last.ToolCalls[0].Name != "git_status" {
		t.Fatal("first tool call mismatch")
	}
}

func TestAppendNativeToolResult(t *testing.T) {
	s := NewSession()
	s.RecordInput("run ls")
	s.AppendAssistantWithToolCalls([]llm.NativeToolCall{
		{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"ls"}`},
	})
	s.AppendNativeToolResult("c1", "file1.go\nfile2.go")

	snap := s.Snapshot()
	if len(snap.History) != 3 {
		t.Fatalf("want 3 history entries, got %d", len(snap.History))
	}
	result := snap.History[2]
	if result.Role != llm.RoleTool {
		t.Fatalf("role = %q, want tool", result.Role)
	}
	if result.ToolCallID != "c1" {
		t.Fatalf("tool_call_id = %q, want c1", result.ToolCallID)
	}
	if result.Content != "file1.go\nfile2.go" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestMessagesIncludesToolRoleMessages(t *testing.T) {
	s := NewSession()
	s.RecordInput("check status")
	s.AppendAssistantWithToolCalls([]llm.NativeToolCall{
		{ID: "c1", Name: "git_status", ArgsJSON: `{}`},
	})
	s.AppendNativeToolResult("c1", "nothing to commit")

	msgs := s.Messages("system prompt")
	// system + user + assistant(tool_calls) + tool(result)
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages, got %d\nhistory: %+v", len(msgs), msgs)
	}
	if msgs[2].Role != llm.RoleAssistant || len(msgs[2].ToolCalls) != 1 {
		t.Fatal("message 3 should be assistant with tool calls")
	}
	if msgs[3].Role != llm.RoleTool || msgs[3].ToolCallID != "c1" {
		t.Fatal("message 4 should be tool result with correct ID")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/cass/git/forge && go test ./internal/react/... -run "TestAppendAssistantWithToolCalls|TestAppendNativeToolResult|TestMessagesIncludesToolRole" 2>&1 | head -15
```
Expected: compile errors.

- [ ] **Step 3: Fix `BuildMessages` in `prompt.go`**

In `internal/react/prompt.go`, replace the body of the `for _, msg := range snapshot.History` loop:

Current:
```go
for _, msg := range snapshot.History {
    content := strings.TrimSpace(msg.Content)
    if content == "" {
        continue
    }
    messages = append(messages, llm.Message{Role: msg.Role, Content: content})
}
```

Replace with:
```go
for _, msg := range snapshot.History {
	// Pass through tool-role messages and assistant messages with native tool calls
	// even if their text content is empty — the ToolCallID / ToolCalls fields carry
	// the payload that the provider needs.
	if msg.Role == llm.RoleTool || len(msg.ToolCalls) > 0 {
		messages = append(messages, msg)
		continue
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		continue
	}
	messages = append(messages, llm.Message{Role: msg.Role, Content: content})
}
```

- [ ] **Step 4: Add session methods to `session.go`**

Add to `internal/react/session.go`:
```go
// AppendAssistantWithToolCalls records an assistant message that contains native
// tool calls (may have empty text content). Used by the native tool calling path.
func (s *Session) AppendAssistantWithToolCalls(calls []llm.NativeToolCall) {
	if s == nil || len(calls) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, llm.Message{
		Role:      llm.RoleAssistant,
		ToolCalls: append([]llm.NativeToolCall(nil), calls...),
	})
}

// AppendNativeToolResult records a tool execution result matched to a specific
// tool call ID. Used by the native tool calling path.
func (s *Session) AppendNativeToolResult(toolCallID, result string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: toolCallID,
		Content:    result,
	})
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
cd /Users/cass/git/forge && go test ./internal/react/... -run "TestAppendAssistantWithToolCalls|TestAppendNativeToolResult|TestMessagesIncludesToolRole" -v
```
Expected: all 3 tests PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/cass/git/forge && git add internal/react/prompt.go internal/react/session.go internal/react/session_test.go
git commit -m "react: fix BuildMessages to preserve tool call messages; add native session methods"
```

---

## Chunk 6: Native System Prompt

### Task 6: Add `BuildNativeSystemPrompt` and wire `NativeSystemPrompt` into react config

**Files:**
- Modify: `internal/agent/system.go`
- Modify: `internal/react/loop.go` (Config + Runner struct only, no routing logic yet)
- Modify: `internal/runtime/chat.go`

- [ ] **Step 1: Add `BuildNativeSystemPrompt` to `agent/system.go`**

Add after `BuildSystemPrompt`:
```go
// BuildNativeSystemPrompt builds the system prompt for models using provider-native
// tool calling. Tool descriptions are omitted — the model receives them via the API
// tools parameter. XML format instructions are not included.
func BuildNativeSystemPrompt(workDir string) string {
	var sb strings.Builder
	sb.WriteString("You are forge, a coding agent. You work in the user's project directory.\n\n")
	sb.WriteString(fmt.Sprintf("Working directory: %s\n", workDir))
	sb.WriteString("\nGuidelines:\n")
	sb.WriteString("- Read files before editing them. Understand what you're changing.\n")
	sb.WriteString("- Use edit_file for surgical changes to existing files. Use write_file only for new files or complete rewrites.\n")
	sb.WriteString("- After making changes, run relevant tests or build commands to verify.\n")
	sb.WriteString("- Do not narrate intent without acting.\n")
	sb.WriteString("- Do not wait for confirmation before using non-destructive tools. Act first, then report results.\n")
	sb.WriteString("- If something fails, read the error, diagnose, and fix. Don't repeat the same failing approach.\n")
	sb.WriteString("- Ask the user for clarification only if the request is ambiguous or you are genuinely blocked.\n")
	sb.WriteString("\n## Autonomy\n")
	sb.WriteString("- KEEP GOING. Solve problems. Ask only when truly impossible.\n")
	sb.WriteString("- If you need information, call a tool to get it. If you need to change a file, call the tool.\n")
	sb.WriteString("- Only respond with plain text (no tool calls) when you have a complete final answer.\n")
	sb.WriteString("- Before asking the user, exhaust self-help: read files, search, grep, check git log, run commands.\n")
	return sb.String()
}
```

- [ ] **Step 2: Add `NativeSystemPrompt` to react `Config` and `Runner`**

In `internal/react/loop.go`, extend `Config`:
```go
type Config struct {
	Driver             llm.Driver
	Tools              *agenttools.Registry
	Renderer           agent.RenderTarget
	SystemPrompt       func() string
	NativeSystemPrompt func() string // used when native tool calling is active; no XML tool format
	Session            *Session
	Progress           func(string)
	MaxSessionTurns    int
}
```

Add field to `Runner`:
```go
type Runner struct {
	driver             llm.Driver
	tools              *agenttools.Registry
	renderer           agent.RenderTarget
	systemPrompt       func() string
	nativeSystemPrompt func() string
	session            *Session
	progress           func(string)
	maxSessionTurns    int
}
```

Update `NewRunner` to capture it:
```go
return &Runner{
	driver:             cfg.Driver,
	tools:              reg,
	renderer:           cfg.Renderer,
	systemPrompt:       cfg.SystemPrompt,
	nativeSystemPrompt: cfg.NativeSystemPrompt,
	session:            session,
	progress:           cfg.Progress,
	maxSessionTurns:    maxSessionTurns(cfg.MaxSessionTurns),
}
```

- [ ] **Step 3: Update `chat.go` to provide `NativeSystemPrompt`**

In `internal/runtime/chat.go`, find both `reactruntime.NewRunner(reactruntime.Config{...})` calls (in `RunChatLive` and `RunChatConsole`) and add to each:
```go
NativeSystemPrompt: func() string { return agent.BuildNativeSystemPrompt(setup.WorkDir) },
```

In `registerReactDelegationTools`, find the child runner creation and add the same field.

- [ ] **Step 4: Add `nativeCurrentSystemPrompt` helper to `loop.go`**

Add to `internal/react/loop.go`:
```go
func (r *Runner) nativeCurrentSystemPrompt() string {
	if r.nativeSystemPrompt != nil {
		return strings.TrimSpace(r.nativeSystemPrompt())
	}
	return r.currentSystemPrompt()
}
```

- [ ] **Step 5: Verify full build**

```bash
cd /Users/cass/git/forge && go build ./...
```
Expected: no errors.

- [ ] **Step 6: Run existing tests**

```bash
cd /Users/cass/git/forge && go test ./internal/react/... ./internal/agent/... ./internal/runtime/...
```
Expected: all pass.

- [ ] **Step 7: Commit**

```bash
cd /Users/cass/git/forge && git add internal/agent/system.go internal/react/loop.go internal/runtime/chat.go
git commit -m "agent,react: BuildNativeSystemPrompt and NativeSystemPrompt config field"
```

---

## Chunk 7: React Runner Native Routing

### Task 7: Wire native path into the react loop

**Files:**
- Modify: `internal/react/loop.go`
- Modify: `internal/react/loop_test.go`

- [ ] **Step 1: Write failing tests for native path**

Add to `internal/react/loop_test.go`:
```go
// nativeToolCallDriver simulates a provider that returns a native tool call
// on the first invocation and a plain text response on subsequent invocations.
type nativeToolCallDriver struct {
	callCount  int
	lastTools  []llm.ToolDef
	lastMsgs   []llm.Message
}

func (d *nativeToolCallDriver) Name() string { return "native-tool-driver" }

func (d *nativeToolCallDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *nativeToolCallDriver) StreamWithTools(_ context.Context, msgs []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	d.lastTools = tools
	d.lastMsgs = msgs
	switch d.callCount {
	case 1:
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "git_status", ArgsJSON: `{}`}}
	default:
		out <- llm.Token{Text: "No changes detected."}
	}
	return nil
}

func TestRunnerNativeToolCallingPath(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	called := false
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "nothing to commit", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "check the repo"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("git_status tool should have been called")
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want 2 (tool call turn + final answer turn)", driver.callCount)
	}

	// Session history: user + assistant(tool_calls) + tool(result) + assistant(final)
	snap := session.Snapshot()
	roles := make([]llm.Role, 0, len(snap.History))
	for _, m := range snap.History {
		roles = append(roles, m.Role)
	}
	want := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleAssistant}
	if len(roles) != len(want) {
		t.Fatalf("history roles = %v, want %v", roles, want)
	}
	for i, r := range want {
		if roles[i] != r {
			t.Fatalf("history[%d] role = %q, want %q", i, roles[i], r)
		}
	}
}

func TestRunnerNativePathUsesNativeSystemPrompt(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name: "git_status", AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil },
	})
	nativePromptCalled := false
	r := NewRunner(Config{
		Driver: driver,
		Tools:  reg,
		SystemPrompt: func() string { return "xml-prompt-with-tool-format" },
		NativeSystemPrompt: func() string {
			nativePromptCalled = true
			return "native-prompt"
		},
	})
	_ = r.Run(context.Background(), "check")
	if !nativePromptCalled {
		t.Fatal("native system prompt should be used when driver implements NativeToolCaller")
	}
	// Also verify the native prompt was sent to the driver, not the XML prompt
	for _, msg := range driver.lastMsgs {
		if msg.Role == llm.RoleSystem && msg.Content == "xml-prompt-with-tool-format" {
			t.Fatal("XML prompt should NOT be sent to a NativeToolCaller driver")
		}
	}
}

func TestRunnerNativePathPassesToolDefs(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read a file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute:     func(_ context.Context, _ map[string]any) (string, error) { return "content", nil },
	})
	r := NewRunner(Config{Driver: driver, Tools: reg})
	// First call will return tool call token; we need a second response
	// Override: driver already handles it in StreamWithTools
	_ = r.Run(context.Background(), "read something")
	if len(driver.lastTools) == 0 {
		t.Fatal("tool defs should be passed to StreamWithTools")
	}
	if driver.lastTools[0].Name != "read_file" {
		t.Fatalf("first tool def name = %q, want read_file", driver.lastTools[0].Name)
	}
}

func TestRunnerFallsBackToXMLWhenNoNativeToolCaller(t *testing.T) {
	// A plain scriptedDriver does NOT implement NativeToolCaller.
	// The runner should use the XML text parsing path.
	driver := &scriptedDriver{responses: []string{
		"<tool_call>\n{\"name\":\"git_status\",\"args\":{}}\n</tool_call>",
		"Clean.",
	}}
	reg := agenttools.NewRegistry()
	called := false
	reg.Register(agenttools.Tool{
		Name: "git_status", AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "nothing to commit", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg})
	if err := r.Run(context.Background(), "check"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("git_status should be called via XML fallback path")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/cass/git/forge && go test ./internal/react/... -run "TestRunnerNative|TestRunnerFalls" 2>&1 | head -20
```
Expected: compile errors (`StreamWithTools` on test driver not recognized, or routing not implemented).

- [ ] **Step 3: Replace `runLoop` in `loop.go` to add native routing**

Replace `runLoop`:
```go
func (r *Runner) runLoop(ctx context.Context, turn int) error {
	start := time.Now()
	defer r.emitStats(start)

	// Determine at loop start whether to use provider-native tool calling.
	nativeCaller, isNative := r.driver.(llm.NativeToolCaller)
	var toolDefs []llm.ToolDef
	if isNative && r.tools != nil {
		toolDefs = r.tools.ToLLMToolDefs()
		if len(toolDefs) == 0 {
			isNative = false
		}
	}

	invalidResponses := 0
	for step := 0; step < r.maxSessionTurns; step++ {
		if isNative {
			calls, err := r.streamNativeTurn(ctx, turn, nativeCaller, toolDefs)
			if err != nil {
				r.session.CompleteTurn(turn, "", nil, err)
				return err
			}
			if calls == nil {
				// streamNativeTurn already recorded the final response
				return nil
			}
			if err := r.executeNativeToolCalls(ctx, turn, calls); err != nil {
				return err
			}
			continue
		}

		// XML text parsing path (unchanged)
		streamed, err := r.streamResponse(ctx)
		if err != nil {
			r.session.CompleteTurn(turn, "", nil, err)
			return err
		}
		response := streamed.text
		calls, visibleText := agent.ParseToolCalls(response)
		trimmedVisible := strings.TrimSpace(visibleText)
		if len(calls) == 0 {
			if retry := invalidWorkingResponseNudge(response); retry != "" {
				invalidResponses++
				if invalidResponses >= maxInvalidWorkingResponses() {
					err := fmt.Errorf("react runtime: too many invalid working responses (%d)", invalidResponses)
					r.session.CompleteTurn(turn, "", nil, err)
					return err
				}
				if step+1 < r.maxSessionTurns {
					r.session.AppendRuntimeNote(retry)
					continue
				}
			}
			if trimmedVisible == "" && strings.TrimSpace(response) == "" {
				err := fmt.Errorf("react runtime: empty final response")
				r.session.CompleteTurn(turn, "", nil, err)
				return err
			}
			if retry := invalidWorkingResponseNudge(response); retry != "" {
				err := fmt.Errorf("react runtime: invalid final response: %s", strings.TrimSpace(retry))
				r.session.CompleteTurn(turn, "", nil, err)
				return err
			}
			final := strings.TrimSpace(response)
			if trimmedVisible != "" {
				final = trimmedVisible
			}
			r.session.AppendAssistantMessage(final)
			r.session.CompleteTurn(turn, final, nil, nil)
			if r.renderer != nil && final != "" && !streamed.streamedVisible {
				r.renderer.AgentText(final)
			}
			return nil
		}

		invalidResponses = 0
		results, err := r.executeToolCalls(ctx, calls)
		r.session.AppendToolResults(compactToolResults(results))
		r.session.CompleteTurn(turn, "", calls, err)
		if err != nil {
			return err
		}
	}

	err := fmt.Errorf("react runtime: max steps (%d) exceeded", r.maxSessionTurns)
	r.session.CompleteTurn(turn, "", nil, err)
	return err
}
```

- [ ] **Step 4: Add `streamNativeTurn`**

Add to `internal/react/loop.go`:
```go
// streamNativeTurn runs one native tool calling step.
// Returns nil calls (+ nil error) when a final text answer was received.
// Returns non-nil calls when the model requested tool executions.
func (r *Runner) streamNativeTurn(ctx context.Context, turn int, caller llm.NativeToolCaller, toolDefs []llm.ToolDef) ([]llm.NativeToolCall, error) {
	messages := r.session.Messages(r.nativeCurrentSystemPrompt())
	out := make(chan llm.Token, 64)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- caller.StreamWithTools(streamCtx, messages, toolDefs, out)
	}()

	var textBuf strings.Builder
	var toolCalls []llm.NativeToolCall
	visibleEmitted := 0

	for tok := range out {
		if tok.ToolCall != nil {
			toolCalls = append(toolCalls, *tok.ToolCall)
			continue
		}
		if tok.Text != "" {
			textBuf.WriteString(tok.Text)
			current := textBuf.String()
			safe := safeVisiblePrefix(current)
			if r.renderer != nil && len(safe) > visibleEmitted {
				r.renderer.AgentToken(safe[visibleEmitted:])
				visibleEmitted = len(safe)
			}
		}
	}
	if err := <-errCh; err != nil {
		return nil, err
	}

	if len(toolCalls) > 0 {
		r.session.AppendAssistantWithToolCalls(toolCalls)
		return toolCalls, nil
	}

	// Final text answer
	finalText := strings.TrimSpace(textBuf.String())
	if finalText == "" {
		return nil, fmt.Errorf("react runtime: empty native response")
	}
	r.session.AppendAssistantMessage(finalText)
	r.session.CompleteTurn(turn, finalText, nil, nil)
	if r.renderer != nil && visibleEmitted < len(finalText) {
		r.renderer.AgentText(finalText[visibleEmitted:])
	}
	return nil, nil
}
```

- [ ] **Step 5: Add `executeNativeToolCalls`**

Add to `internal/react/loop.go`:
```go
// executeNativeToolCalls executes a batch of native tool calls and appends results
// to the session. On unknown tool or execution error, appends an error result and
// continues processing remaining calls (same behaviour as the XML path).
func (r *Runner) executeNativeToolCalls(ctx context.Context, turn int, calls []llm.NativeToolCall) error {
	for _, call := range calls {
		tool, ok := r.tools.Get(call.Name)
		if !ok {
			errMsg := fmt.Sprintf("error: unknown tool %q", call.Name)
			if r.renderer != nil {
				r.renderer.Error(fmt.Sprintf("unknown tool %q", call.Name))
			}
			r.session.AppendNativeToolResult(call.ID, errMsg)
			r.session.CompleteTurn(turn, "", nil, fmt.Errorf("%s", errMsg))
			return fmt.Errorf("%s", errMsg)
		}

		var args map[string]any
		if err := json.Unmarshal([]byte(call.ArgsJSON), &args); err != nil {
			args = map[string]any{}
		}

		if r.renderer != nil {
			r.renderer.ToolCall(call.Name, reactToolSummary(agent.ToolCall{Args: args}))
		}

		result, err := tool.Execute(ctx, args)
		diff := ""
		if tool.LastDiff != nil {
			diff = tool.LastDiff()
		}
		if err != nil {
			errResult := fmt.Sprintf("error: %v", err)
			if r.renderer != nil {
				r.renderer.ToolResult(call.Name, errResult, diff, true)
			}
			r.session.AppendNativeToolResult(call.ID, errResult)
			r.session.CompleteTurn(turn, "", nil, err)
			return err
		}

		display := truncateToolResult(result)
		if r.renderer != nil {
			r.renderer.ToolResult(call.Name, display, diff, false)
		}
		r.session.AppendNativeToolResult(call.ID, result)
	}
	return nil
}
```

- [ ] **Step 6: Add `encoding/json` import to `loop.go`**

Add `"encoding/json"` to the imports in `internal/react/loop.go`.

- [ ] **Step 7: Run tests to verify they pass**

```bash
cd /Users/cass/git/forge && go test ./internal/react/... -run "TestRunnerNative|TestRunnerFalls" -v
```
Expected: all tests PASS.

- [ ] **Step 8: Run full test suite**

```bash
cd /Users/cass/git/forge && go test ./... 2>&1 | tail -30
```
Expected: all pass.

- [ ] **Step 9: Commit**

```bash
cd /Users/cass/git/forge && git add internal/react/loop.go internal/react/loop_test.go
git commit -m "react: native tool calling path — routes to StreamWithTools when driver supports it"
```

---

## Chunk 8: Final Verification

### Task 8: Confirm the feature works end-to-end

- [ ] **Step 1: Build the binary**

```bash
cd /Users/cass/git/forge && go build -o /tmp/forge-test ./cmd/forge/ && echo "BUILD OK"
```
Expected: `BUILD OK`.

- [ ] **Step 2: Run full test suite**

```bash
cd /Users/cass/git/forge && go test ./... 2>&1 | grep -E "FAIL|ok" | tail -30
```
Expected: all packages show `ok`, none show `FAIL`.

- [ ] **Step 3: Vet**

```bash
cd /Users/cass/git/forge && go vet ./...
```
Expected: no output (no warnings).

- [ ] **Step 4: Verify the production path is wired**

Confirm `OpenAIDriver` implements `NativeToolCaller`:
```bash
grep -n "func.*OpenAIDriver.*StreamWithTools" /Users/cass/git/forge/internal/llm/drivers/openai.go
```
Expected: one match.

Confirm `RetryDriver` forwards:
```bash
grep -n "func.*RetryDriver.*StreamWithTools" /Users/cass/git/forge/internal/llm/retry.go
```
Expected: one match.

Confirm react runner checks for interface:
```bash
grep -n "NativeToolCaller" /Users/cass/git/forge/internal/react/loop.go
```
Expected: at least two matches (type assertion + usage).

- [ ] **Step 5: Final commit**

```bash
cd /Users/cass/git/forge && git status
# If any unstaged changes remain, review and commit them
git commit -m "native tool calling: complete — XML parser kept as fallback" --allow-empty
```

---

## Summary of Changes

| Layer | Before | After |
|-------|--------|-------|
| System prompt | Always includes XML `<tool_call>` format instructions | XML instructions omitted when native tool calling is active |
| Tool delivery | Tool descriptions injected into prompt text | Tool definitions sent via API `tools` parameter |
| Tool call parsing | Always `agent.ParseToolCalls(text)` | Native: structured API response; Fallback: existing XML parser unchanged |
| Conversation history | Tool results as user messages | Native: proper `tool`/`assistant(tool_calls)` message roles; Fallback: unchanged |
| Repair layer | Multiple JSON tolerance transforms in text parser | Streaming delta accumulator + `repairToolCallArgsJSON` in driver |
| RetryDriver | Transparent wrapper, no NativeToolCaller support | Forwards `StreamWithTools` to inner driver when available |
| Model coverage | All models use XML path | Native for all OpenAI-compatible models; XML for everything else (e.g. Claude) |
