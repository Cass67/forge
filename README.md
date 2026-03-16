# forge

Write code with one LLM, audit it with another. Repeat until it's production-ready.

## How it works

forge runs your idea through four sequential passes:

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

```bash
export ANTHROPIC_API_KEY=sk-ant-...
export OPENAI_API_KEY=sk-...
```

Optional config file at `~/.config/forge/config.toml`:

```toml
[models]
writer     = "claude-sonnet-4-6"
auditor    = "gpt-4o"
summarizer = "claude-haiku-4-5-20251001"

[session]
rounds_per_pass = 3
output_dir = "~/forge-output"
```

## Keybindings

| Key | Action |
|-----|--------|
| `v` | Toggle split pane / yolo view |
| `p` | Pause / resume |
| `s` | Snapshot current code |
| `q` | Quit |
