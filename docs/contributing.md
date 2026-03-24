# Contributing To Forge

This document explains how to work effectively in the Forge repository.

## Repository Shape

Important top-level areas:

- [cmd/forge/main.go](/Users/cass/git/forge/cmd/forge/main.go): CLI entrypoint
- [internal/bootstrap/](/Users/cass/git/forge/internal/bootstrap): config loading, model discovery, provider routing
- [internal/runtime/](/Users/cass/git/forge/internal/runtime): chat and session assembly
- [internal/agent/](/Users/cass/git/forge/internal/agent): core agent loop, sub-agents, prompts, rendering
- [internal/agent/tools/](/Users/cass/git/forge/internal/agent/tools): tool implementations
- [internal/tui/](/Users/cass/git/forge/internal/tui): Bubble Tea frontends and UI models
- [internal/llm/](/Users/cass/git/forge/internal/llm): driver interfaces, retries, events, usage
- [internal/llm/drivers/](/Users/cass/git/forge/internal/llm/drivers): provider-specific implementations
- [internal/session/](/Users/cass/git/forge/internal/session): pass-based improvement runner
- [internal/config/](/Users/cass/git/forge/internal/config): config schema and persistence
- [docs/](/Users/cass/git/forge/docs): repo documentation

## Local Setup

Minimum:

```bash
go build ./...
go test ./...
```

For hook-compatible development, install the tools listed in [LOCAL_TOOLING.md](/Users/cass/git/forge/LOCAL_TOOLING.md).

## Pre-Commit Hooks

This repository uses local hooks from [.pre-commit-config.yaml](/Users/cass/git/forge/.pre-commit-config.yaml).

Important checks include:

- risky filename detection
- merge conflict marker detection
- trailing whitespace detection
- private key marker detection
- `gitleaks`
- `gofmt`
- `goimports`
- `go vet`
- `golangci-lint`
- `govulncheck`

If your commit fails, read the hook output directly. The most common issues are formatting drift, lint failures, or missing local tooling.

## Recommended Workflow

For code changes:

1. read the relevant runtime path first
2. add or update tests before changing behavior
3. keep changes scoped to one concern
4. run targeted tests
5. run `go build ./...`
6. run `go test ./...`

For provider/model changes, always re-check:

- [internal/bootstrap/runtime.go](/Users/cass/git/forge/internal/bootstrap/runtime.go)
- provider-specific driver tests
- model picker behavior in [internal/tui/chatmodel.go](/Users/cass/git/forge/internal/tui/chatmodel.go)

For chat behavior changes, always re-check:

- [internal/runtime/chat.go](/Users/cass/git/forge/internal/runtime/chat.go)
- [internal/agent/agent.go](/Users/cass/git/forge/internal/agent/agent.go)
- [internal/tui/chatmodel.go](/Users/cass/git/forge/internal/tui/chatmodel.go)

## Adding A Tool

Tools live in [internal/agent/tools/](/Users/cass/git/forge/internal/agent/tools).

Typical steps:

1. implement the tool
2. register it in `registerTools(...)` in [internal/runtime/chat.go](/Users/cass/git/forge/internal/runtime/chat.go)
3. decide whether it is prompt-core or prompt-hidden
4. update tests
5. if multi-agent access matters, update allowed tool lists in [internal/agent/roles.go](/Users/cass/git/forge/internal/agent/roles.go)

## Adding Or Changing A Provider

Provider work usually touches several layers:

- config and stored credentials
- provider auth flow
- driver implementation
- model discovery
- bootstrap routing
- provider picker / model picker behavior
- tests

Common files:

- [internal/auth/store.go](/Users/cass/git/forge/internal/auth/store.go)
- [internal/bootstrap/runtime.go](/Users/cass/git/forge/internal/bootstrap/runtime.go)
- [internal/llm/drivers/](/Users/cass/git/forge/internal/llm/drivers)
- [internal/tui/chatmodel.go](/Users/cass/git/forge/internal/tui/chatmodel.go)

Do not treat model naming, auth, and request shape as separable by default. In practice they often need to change together.

## Multi-Agent Work

Multi-agent behavior is not just a prompt change. It crosses:

- role definitions in [internal/agent/roles.go](/Users/cass/git/forge/internal/agent/roles.go)
- delegation in [internal/agent/subagent.go](/Users/cass/git/forge/internal/agent/subagent.go)
- runtime setup in [internal/runtime/chat.go](/Users/cass/git/forge/internal/runtime/chat.go)
- chat rendering in [internal/tui/chatmodel.go](/Users/cass/git/forge/internal/tui/chatmodel.go)

If you change delegation behavior, verify both:

- tool-pane audit detail
- main-pane chat summaries / recent activity

## Documentation Expectations

If behavior changes in any of these areas, update docs in the same change:

- user-facing commands or setup: [README.md](/Users/cass/git/forge/README.md)
- architectural behavior: [docs/architecture.md](/Users/cass/git/forge/docs/architecture.md)
- build workflow: [docs/build.md](/Users/cass/git/forge/docs/build.md)

## Testing Guidance

Prefer targeted tests while iterating, then run the full suite before committing.

Examples:

```bash
go test ./internal/agent -run TestAgentRunDoesNotRetryShortFinalAnswer
go test ./internal/bootstrap -run TestCanonicalAnthropicModel
go test ./internal/tui -run TestChatModelModelsOverlayLeadingDigitStartsSearch
```

Final verification:

```bash
go build ./...
go test ./...
```

## Security And Secrets

This repository has strict handling rules for secret-bearing paths and auth material.

Before touching auth or config code, read:

- [AGENTS.md](/Users/cass/git/forge/AGENTS.md)

Key rule: do not print or copy secrets into code, docs, logs, fixtures, or commits.
