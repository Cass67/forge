# ChatGPT And OpenAI Provider Paths

Forge now supports two separate OpenAI-family paths:

- `chatgpt/<model>` uses ChatGPT/Codex subscription auth
- `openai/<model>` uses the native OpenAI Platform API key

Examples:

- `chatgpt/gpt-5.5`
- `chatgpt/gpt-5.3-codex`
- `openai/gpt-5.4`

## Default Resolution

For unqualified GPT-family models such as `gpt-5.4`, Forge now prefers providers in this order:

1. ChatGPT subscription path, when local ChatGPT/Codex auth is available and the model is supported there
2. GitHub Copilot, when a Copilot token is available for that model
3. OpenAI API, when `OPENAI_API_KEY` is configured

This keeps plain `gpt-*` model selection usable with a ChatGPT subscription while still allowing explicit API usage through the `openai/` prefix.

## How The ChatGPT Path Works

Forge does not use the `codex` CLI binary for inference.

Instead, the ChatGPT path:

- reads OAuth state from Forge's own auth file:
  - `~/.config/forge/auth.json`
- refreshes the OAuth access token directly against the OpenAI auth endpoint
- sends Responses API requests to:
  - `https://chatgpt.com/backend-api/codex/responses`
- includes `ChatGPT-Account-Id` when the local auth state provides one
- uses WebSocket transport (`wss://`) as the primary streaming path, with automatic HTTP fallback

The implementation lives in:

- [internal/chatgptauth/auth.go](/Users/cass/git/forge/internal/chatgptauth/auth.go)
- [internal/llm/drivers/openai.go](/Users/cass/git/forge/internal/llm/drivers/openai.go)
- [internal/llm/drivers/openai_ws.go](/Users/cass/git/forge/internal/llm/drivers/openai_ws.go)
- [internal/bootstrap/runtime.go](/Users/cass/git/forge/internal/bootstrap/runtime.go)

### WebSocket Transport

For gpt-5.x models, Forge opens a persistent WebSocket connection to `wss://chatgpt.com/backend-api/codex/responses`. This enables:

- Lower-latency streaming (no per-request HTTP overhead)
- Delta-based conversation continuation via `previous_response_id` — only new messages are sent, not the full history
- Automatic fallback to HTTP SSE on connection failure (per-session sticky fallback)

### Stateless / ZDR Mode for gpt-5.x

All `chatgpt/gpt-5.*` models use the stateless Responses API with Zero Data Retention (`store: false`). This means:

- The server does not persist response state
- Encrypted reasoning content (`reasoning.encrypted_content`) is included in API responses
- For HTTP requests: the full conversation history must be re-sent each turn
- For WebSocket requests: `previous_response_id` chaining is supported in-memory despite `store: false`

### API Routing

The driver selects the API endpoint based on model type:

- **gpt-5.x reasoning models** (gpt-5, gpt-5.4, gpt-5.5): always use the Responses API
- **Other models** (gpt-4o, etc.): prefer chat completions with native tools; fall back to Responses API when supported

Reasoning models do not support temperature. The `reasoning.effort` parameter controls how much the model thinks before responding (defaults to `medium` for gpt-5.5).

## Model Selection

The current selection surface is the existing model picker and `/model` command.

Examples:

- `/model chatgpt/gpt-5.5`
- `/model openai/gpt-5.4`

The available model list now includes both prefixed paths when the relevant credentials exist.

## Provider Selection

Live chat now also supports `/provider`.

Examples:

- `/provider`
- `/provider chatgpt`
- `/provider openai`
- `/provider anthropic`
- `/provider copilot`

The provider picker now lists Forge backends that have a routing implementation, including:

- `anthropic`
- `chatgpt`
- `copilot`
- `openai`
- configured OpenAI-compatible backends such as `xai`, `mistral`, `perplexity`, `groq`, `openrouter`, and others

If a provider is not ready yet, `/provider` now prompts for the right setup path:

- login-backed providers use an in-app device flow
  - `chatgpt`
  - `copilot`
- API-key-backed providers prompt for an API key and save it to Forge config

After setup completes, Forge refreshes the backend list and switches to that provider automatically.

## Current Limitation

Provider choice can still always be expressed directly through the model name:

- `chatgpt/...` for ChatGPT subscription
- `openai/...` for OpenAI API
- `copilot/...` for GitHub Copilot

## Auth Storage

When ChatGPT sign-in is started from inside Forge, the OAuth session is stored in Forge's auth file:

- `~/.config/forge/auth.json`

The inference driver reads auth exclusively from Forge's own auth file. The legacy Codex auth files (`~/.config/codex/auth.json`, `~/.codex/auth.json`) are only used as a fallback by the `/stats` Codex usage lookup — not by the main inference path.
