# ChatGPT And OpenAI Provider Paths

Forge now supports two separate OpenAI-family paths:

- `chatgpt/<model>` uses ChatGPT/Codex subscription auth
- `openai/<model>` uses the native OpenAI Platform API key

Examples:

- `chatgpt/gpt-5.4`
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

- reads local ChatGPT/Codex OAuth state from:
  - `~/.config/codex/auth.json`, or
  - `~/.codex/auth.json`
- refreshes the OAuth access token directly against the OpenAI auth endpoint
- sends Responses API-shaped requests to:
  - `https://chatgpt.com/backend-api/codex/responses`
- includes `ChatGPT-Account-Id` when the local auth state provides one

The implementation lives in:

- [internal/chatgptauth/auth.go](/Users/cass/git/forge/internal/chatgptauth/auth.go)
- [internal/llm/drivers/openai.go](/Users/cass/git/forge/internal/llm/drivers/openai.go)
- [internal/bootstrap/runtime.go](/Users/cass/git/forge/internal/bootstrap/runtime.go)

## Model Selection

The current selection surface is the existing model picker and `/model` command.

Examples:

- `/model chatgpt/gpt-5.4`
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

When ChatGPT sign-in is started from inside Forge, the OAuth session is stored in Forge’s auth file:

- `~/.config/forge/auth.json`

Forge still supports reading existing local Codex auth state as a fallback, but it no longer requires the `codex` CLI to be installed in order to authenticate or run inference through the ChatGPT path.
