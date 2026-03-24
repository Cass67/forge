# Custom Provider Files Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Forge-managed custom OpenAI-compatible providers loaded from `~/.config/forge/providers/*.toml` and compatible root-level `~/.config/forge/*.toml` files, with API-key auth stored in Forge `auth.json` and models exposed through the existing provider/model pickers.

**Architecture:** Extend the existing compat-provider path rather than inventing a new provider stack. A new loader in `internal/bootstrap` will parse provider definitions from disk into normalized compat-provider descriptors, auth storage will gain a dynamic API-key map for non-hardcoded providers, and the runtime/TUI will continue using the current generic API-key prompt flow with only small changes to support dynamic provider ids.

**Tech Stack:** Go, BurntSushi TOML, existing Forge auth/config/bootstrap/runtime/TUI packages, OpenAI-compatible driver layer.

---

### Task 1: Define custom provider file schema and loader tests

**Files:**
- Create: `internal/bootstrap/custom_providers.go`
- Create: `internal/bootstrap/custom_providers_test.go`

- [ ] **Step 1: Write failing loader tests for both supported discovery locations**

Add tests covering:
- `~/.config/forge/providers/*.toml`
- root-level `~/.config/forge/*.toml` files containing `[model_providers.<id>]`
- ignoring unrelated TOML files without provider blocks
- normalizing provider ids, labels, base URLs, models, default models, `wire_api`, and `http_headers`

Run: `go test ./internal/bootstrap -run 'TestLoadCustomCompatProviders' -v`
Expected: FAIL because the loader does not exist yet.

- [ ] **Step 2: Implement file-backed provider parsing**

Implement a loader that:
- reads provider definitions from both locations
- accepts the existing Codex-style block shape:

```toml
[model_providers.oca]
name = "My New Provider"
base_url = "https://example.com/v1"
wire_api = "responses"
http_headers = { client = "codex-cli" }
default_model = "gpt-5.4"
models = ["gpt-5.4", "gpt-5.4-mini"]
```

- ignores unrelated top-level keys like `model`, `profile`, `sandbox_mode`, and `[profiles.*]`
- normalizes missing schemes by prepending `https://` when the base URL is bare

- [ ] **Step 3: Run loader tests to green**

Run: `go test ./internal/bootstrap -run 'TestLoadCustomCompatProviders' -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/bootstrap/custom_providers.go internal/bootstrap/custom_providers_test.go
git commit -m "feat: load custom provider definitions from config files"
```

### Task 2: Add dynamic API-key storage for custom providers

**Files:**
- Modify: `internal/auth/store.go`
- Create: `internal/auth/store_test.go`

- [ ] **Step 1: Write failing auth-store tests for dynamic provider keys**

Add tests covering:
- saving a custom provider key into auth state
- loading it back from `auth.json`
- merging new custom-provider keys without clobbering existing built-in keys
- clearing a custom provider key with `SaveExact`

Run: `go test ./internal/auth -run 'TestCustomProviderAPIKeys' -v`
Expected: FAIL because the auth struct has no dynamic provider-key map yet.

- [ ] **Step 2: Extend the auth token model**

Add a JSON field like:

```go
ProviderAPIKeys map[string]string `json:"provider_api_keys,omitempty"`
```

Update merge behavior so:
- new custom provider keys are merged by id
- built-in typed keys remain backward compatible
- blank or missing custom keys do not erase unrelated entries during `Save`

- [ ] **Step 3: Add small helper methods for dynamic provider keys**

Add helpers so the rest of the code can consistently:
- get a custom provider key by provider id
- set a custom provider key
- clear a custom provider key

- [ ] **Step 4: Run auth-store tests to green**

Run: `go test ./internal/auth -run 'TestCustomProviderAPIKeys' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/store.go internal/auth/store_test.go
git commit -m "feat: store custom provider API keys in auth state"
```

### Task 3: Integrate custom providers into compat-provider routing

**Files:**
- Modify: `internal/bootstrap/runtime.go`
- Modify: `internal/bootstrap/resolve.go`
- Modify: `internal/bootstrap/resolve_test.go`

- [ ] **Step 1: Write failing bootstrap tests for custom provider routing**

Add tests covering:
- `BuildCompatProviders` appends custom file-backed providers
- `SupportedProviderBackends` shows a custom provider with `configure API key` before auth
- `AvailableModels` exposes `provider/model` entries only when the provider has a key
- `DriverForModel` resolves `provider/model` for custom providers
- `ResolvedProviderID` and `ModelDisplayLabel` show the custom provider id correctly

Run: `go test ./internal/bootstrap -run 'TestCustomCompatProvider' -v`
Expected: FAIL because file-backed providers are not part of compat-provider routing yet.

- [ ] **Step 2: Extend compat-provider descriptors**

Update `CompatProvider` to carry:
- display label
- optional custom headers
- whether the provider prefers the Responses API

Append loaded custom providers after built-in compat providers.

- [ ] **Step 3: Support explicit custom provider prefixes**

Update provider parsing/resolution so model names like:
- `oca/gpt-5.4`
- `mynewprovider/gpt-5.4-mini`

resolve as explicit compat providers even though those ids are not hardcoded built-ins.

- [ ] **Step 4: Gate custom models on provider readiness**

Custom provider models should appear in `AvailableModels` only when:
- a Forge-stored API key exists for that provider id, or
- an optional environment override declared by the file is present

- [ ] **Step 5: Run bootstrap tests to green**

Run: `go test ./internal/bootstrap -run 'TestCustomCompatProvider' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/bootstrap/runtime.go internal/bootstrap/resolve.go internal/bootstrap/resolve_test.go
git commit -m "feat: route custom provider models through compat bootstrap"
```

### Task 4: Add custom headers and responses-mode support to the OpenAI-compatible driver

**Files:**
- Modify: `internal/llm/drivers/openai.go`
- Modify: `internal/llm/drivers/openai_internal_test.go`

- [ ] **Step 1: Write failing driver tests for custom provider headers and wire API**

Add tests covering:
- custom request headers from provider file config are attached to every request
- `wire_api = "responses"` enables the Responses API path
- default compat providers keep their current behavior
- custom providers do not accidentally inherit ChatGPT-specific stateless responses behavior

Run: `go test ./internal/llm/drivers -run 'TestCustomCompatProvider' -v`
Expected: FAIL because the driver constructor does not yet accept provider-specific headers or response-mode configuration.

- [ ] **Step 2: Extend the OpenAI-compatible driver constructors**

Add a constructor path that accepts:
- provider label
- base URL
- supports-responses flag
- custom headers map

Keep existing constructors intact for built-ins and tests.

- [ ] **Step 3: Ensure custom responses-mode remains generic**

For custom OpenAI-compatible providers:
- allow the Responses API when declared
- do not treat them as ChatGPT stateless providers
- do not enable response-state store/compaction unless already supported by generic logic

- [ ] **Step 4: Run driver tests to green**

Run: `go test ./internal/llm/drivers -run 'TestCustomCompatProvider' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/llm/drivers/openai.go internal/llm/drivers/openai_internal_test.go
git commit -m "feat: support custom compat provider headers and wire modes"
```

### Task 5: Wire dynamic provider auth into the provider picker

**Files:**
- Modify: `internal/tui/chatshared.go`
- Modify: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Write failing TUI tests for custom API-key providers**

Add tests covering:
- a custom provider appears in the provider picker with `configure API key`
- entering a key stores it under the dynamic auth map
- the provider refreshes to `ready`
- the default model switches after save when the provider has `default_model`
- delete removes the stored custom provider credential cleanly

Run: `go test ./internal/tui -run 'TestChatModelCustomProvider' -v`
Expected: FAIL because the current token helpers only know about hardcoded provider ids.

- [ ] **Step 2: Extend generic provider token helpers**

Update:
- `providerUsesAPIKey(...)`
- `setProviderToken(...)`
- `clearProviderToken(...)`
- `providerHasStoredCredential(...)`

so unknown non-interactive providers use the dynamic custom-provider key map automatically.

- [ ] **Step 3: Run TUI tests to green**

Run: `go test ./internal/tui -run 'TestChatModelCustomProvider' -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/tui/chatshared.go internal/tui/chatmodel_test.go
git commit -m "feat: support custom providers in the provider picker"
```

### Task 6: Document the feature and verify the whole repo

**Files:**
- Modify: `README.md`
- Optionally modify: `ARCHITECTURE.md`

- [ ] **Step 1: Update documentation**

Document:
- custom provider file locations
- accepted provider block format
- how API keys are entered and stored in Forge auth
- current v1 constraint: OpenAI-compatible providers only
- current v1 model requirement: `models = [...]` or `default_model` must be present for picker entries

- [ ] **Step 2: Run targeted verification**

Run:

```bash
go test ./internal/auth ./internal/bootstrap ./internal/llm/drivers ./internal/tui -v
```

Expected: PASS

- [ ] **Step 3: Run full verification**

Run:

```bash
go build ./...
go test ./...
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add README.md ARCHITECTURE.md
git commit -m "docs: describe custom provider files"
```

### Task 7: Final integration pass

**Files:**
- Modify any files touched by previous tasks if final cleanup is required

- [ ] **Step 1: Manually exercise the real provider file shape**

Verify with the actual example shape:

```toml
[model_providers.oca]
base_url = "myurl.com"
http_headers = { "client" = "codex-cli", "client-version" = "0" }
name = "my new provider"
wire_api = "responses"
default_model = "gpt-5.4"
models = ["gpt-5.4"]
```

Expected behavior:
- provider picker shows `My new provider`
- status is `configure API key` before auth
- entering the key writes it into `auth.json`
- model picker shows `oca/gpt-5.4` once the key is saved

- [ ] **Step 2: Run final full verification again if any cleanup was needed**

Run:

```bash
go build ./...
go test ./...
```

Expected: PASS

- [ ] **Step 3: Final commit**

```bash
git add internal/auth/store.go internal/auth/store_test.go \
  internal/bootstrap/custom_providers.go internal/bootstrap/custom_providers_test.go \
  internal/bootstrap/runtime.go internal/bootstrap/resolve.go internal/bootstrap/resolve_test.go \
  internal/llm/drivers/openai.go internal/llm/drivers/openai_internal_test.go \
  internal/tui/chatshared.go internal/tui/chatmodel_test.go \
  README.md ARCHITECTURE.md
git commit -m "feat: add file-backed custom model providers"
```
