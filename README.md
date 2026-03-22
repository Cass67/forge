# Forge

Write code with one LLM, audit it with another. Repeat until it's production-ready.

## How it works

Forge runs your idea through four sequential passes:

1. **Correctness** — implement the goal
2. **Refactor** — clean the code
3. **Security** — harden against OWASP top 10
4. **Prod-ready** — logging, shutdown, config, README

Each pass runs N writer → auditor → summarizer rounds. Output lands in `./output/<timestamp>/`.

## Install

Build from source:

```bash
git clone ...
cd forge
go build -o forge ./cmd/forge
```

## Configuration

Config file lives at `~/.config/forge/config.toml`. All keys can also be set as environment variables — env vars take precedence.

```toml
[models]
writer     = "claude-sonnet-4-6"
auditor    = "gpt-4o"
summarizer = "claude-haiku-4-5-20251001"

[session]
rounds_per_pass = 3
output_dir = "~/forge-output"

[keys]
anthropic  = "sk-ant-..."
openai     = "sk-..."
```

## Providers

Forge supports mixing models from different providers for writer, auditor, and summarizer roles.

### Anthropic

```bash
export ANTHROPIC_API_KEY=sk-ant-...
```

Models: `claude-sonnet-4-6`, `claude-opus-4-6`, `claude-3-7-sonnet-latest`, `claude-3-5-haiku-latest`

### OpenAI

```bash
export OPENAI_API_KEY=sk-...
```

Models: `gpt-4o`, `gpt-4o-mini`, `gpt-4-turbo`, `o1`, `o1-mini`, `o3-mini`

### GitHub Copilot

Authenticate with your GitHub account using the bundled GitHub OAuth App client ID. Your Copilot subscription covers usage — no separate API key.

If you want to override the bundled client ID, set one of:

```bash
export FORGE_COPILOT_CLIENT_ID=<your-client-id>
```

```toml
[copilot]
client_id = "<your-client-id>"
```

**Authenticate:**

```bash
forge auth copilot
# Visit the URL shown, enter the code, done.
# Token stored in ~/.config/forge/auth.json
```

Models are prefixed with `copilot/` to distinguish them from the same model names on other providers:

```toml
[models]
writer  = "copilot/gpt-4o"
auditor = "copilot/claude-3.7-sonnet"
```

Forge queries Copilot's live `/models` endpoint and merges that with a curated alias list, so the picker still shows the common names people expect even when Copilot only returns versioned snapshots.

Typical available models include: `copilot/gpt-4o`, `copilot/gpt-4.1`, `copilot/gpt-5`, `copilot/o3`, `copilot/o4-mini`, `copilot/claude-3.7-sonnet`, `copilot/claude-sonnet-4.5`, `copilot/gemini-2.5-pro`

### Groq

```bash
export GROQ_API_KEY=gsk_...
```

Models: `llama-3.3-70b-versatile`, `llama-3.1-8b-instant`, `gemma2-9b-it`, `mixtral-8x7b-32768`, `qwen-qwq-32b`

### Mistral

```bash
export MISTRAL_API_KEY=...
```

Models: `mistral-large-latest`, `mistral-small-latest`, `codestral-latest`

### xAI (Grok)

```bash
export XAI_API_KEY=xai-...
```

Models: `grok-3`, `grok-3-mini`, `grok-3-fast`

### NVIDIA NIM

```bash
export NVIDIA_API_KEY=nvapi-...
```

Models use namespaced IDs: `nvidia/llama-3.1-nemotron-51b-instruct`, `meta/llama-3.3-70b-instruct`

### Perplexity

```bash
export PERPLEXITY_API_KEY=pplx-...
```

Models: `sonar-pro`, `sonar`, `sonar-reasoning-pro`

### Cerebras

```bash
export CEREBRAS_API_KEY=csk-...
```

Models: `llama3.1-8b`, `llama3.3-70b`

### OpenRouter

Single key for access to hundreds of models:

```bash
export OPENROUTER_API_KEY=sk-or-...
```

Models use `provider/model` format: `openai/gpt-4o`, `anthropic/claude-sonnet-4-5`, `meta-llama/llama-3.3-70b-instruct:free`

### Together AI

```bash
export TOGETHER_AI_API_KEY=...
```

Models: `meta-llama/Llama-3.3-70B-Instruct-Turbo`, `deepseek-ai/DeepSeek-R1`

### DeepInfra

```bash
export DEEPINFRA_API_KEY=...
```

Models: `meta-llama/Meta-Llama-3.1-8B-Instruct`, `deepseek-ai/DeepSeek-R1-Turbo`

### Mixing providers

Writer and auditor can use completely different providers:

```toml
[models]
writer     = "llama-3.3-70b-versatile"   # Groq
auditor    = "grok-3-mini"               # xAI
summarizer = "claude-3-5-haiku-latest"   # Anthropic
```

### Model disambiguation

Most providers have distinct model name formats. For providers that share the `vendor/model` namespace (Together, DeepInfra, OpenRouter), the first configured provider wins. Configure only one if you use namespaced model IDs.

## Keybindings

| Key | Action |
|-----|--------|
| `v` | Toggle split pane / yolo view |
| `p` | Pause / resume |
| `s` | Snapshot current code |
| `q` | Quit |
