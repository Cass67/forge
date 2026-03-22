# Skills System Design

Skills are markdown files with frontmatter that get injected into the system prompt on demand. They act as dynamic prompt fragments — not code plugins. This is the same pattern used by Claude Code and Codex.

## Skill File Format

Markdown files in `~/.config/forge/skills/` (user-global) or `.forge/skills/` (project-local):

```markdown
---
name: tdd
description: Use when implementing features — write tests first
trigger: manual
---

When implementing a feature:
1. Write a failing test first
2. Implement the minimum code to pass
3. Refactor
```

### Frontmatter Fields

| Field | Required | Values | Description |
|---|---|---|---|
| `name` | yes | string | Unique identifier, used as `/name` slash command |
| `description` | yes | string | One-line summary shown to model and user |
| `trigger` | no | `manual` (default), `auto` | `auto` skills are listed in system prompt for model-initiated activation |

## Architecture

### New Package: `internal/skills/`

Responsible for:
- Scanning skill directories (project-local first, then user-global)
- Parsing frontmatter and returning skill metadata
- Loading full skill content on demand
- Merging project-local and user-global skills (project-local wins on name conflict)

### System Prompt Changes (`agent/system.go`)

`BuildSystemPrompt()` appends a skills section listing available skills by name and description. For `auto` trigger skills, the model can request activation. For `manual` skills, only the user can activate via slash command.

### TUI Changes (`tui/chatlive_commands.go`)

Intercept `/skillname` input:
- Look up skill by name
- Prepend skill content to the user's message (or inject as system context)
- If no matching skill, treat as normal input

### Config Changes (`config/config.go`)

```toml
[skills]
dirs = ["~/.config/forge/skills"]  # additional skill directories
```

Project-local `.forge/skills/` is always checked first without configuration.

## Implementation Plan

| Component | File(s) | ~Lines |
|---|---|---|
| Skill struct + loader | `internal/skills/skills.go` | 150 |
| System prompt integration | `internal/agent/system.go` | 50 |
| Slash commands in TUI | `internal/tui/chatlive_commands.go` | 30 |
| Skill listing (`/skills`) | `internal/tui/chatlive_commands.go` | 20 |
| Config for extra skill dirs | `internal/config/config.go` | 10 |
| **Total** | | **~260** |

## Future Extensions

- **Skill tool**: Register a meta-tool so the agent can load skills mid-conversation without user intervention.
- **Bundled skills**: Embed default skills via `//go:embed` alongside existing prompts.
- **Template variables**: Support `{{workdir}}`, `{{language}}`, `{{model}}` substitution in skill content.
- **Skill parameters**: Allow skills to declare parameters that the user or model can fill in.
- **Skill chaining**: One skill can reference another by name.
