# Claude Provider Login Design

## Goal

Add a new unsupported `claude` provider that authenticates against `claude.ai` using a browser OAuth authorization-code flow, while keeping `anthropic` as the API-key-backed provider.

## Scope

- Add a new `claude` provider backend and model prefix
- Store Claude OAuth state in Forge-owned auth storage only
- Support browser login with manual paste of the returned code or callback URL
- Route `claude/<model>` through a subscription-backed Claude driver
- Keep `anthropic/<model>` unchanged and API-key-only

## Non-Goals

- No localhost callback listener
- No reuse of Claude Desktop, Codex, or external auth stores
- No claim of official Anthropic support for this provider
- No changes to the existing Anthropic API-key provider semantics

## Product Shape

- `claude`: Claude.ai subscription login
- `anthropic`: Anthropic API key

The provider picker must present these as distinct backends so users can tell which path they are using and delete credentials consistently.

## Auth Flow

Forge adds a new `internal/claudeauth` package that mirrors the structure of `internal/chatgptauth`:

- `StartAuth()` generates a PKCE verifier and authorization URL for `claude.ai/oauth/authorize`
- Forge displays the URL in the provider picker and instructs the user to paste either the returned authorization code or the full callback URL
- `Exchange()` accepts the pasted input, extracts the code if needed, and exchanges it at `https://console.anthropic.com/v1/oauth/token`
- `Manager` refreshes the access token using the stored refresh token before expiry
- `Load()` succeeds only from Forge auth storage
- `StoreSession()` persists the session back into Forge auth storage

This path is intentionally browser-driven but callback-free. It avoids local listener complexity while still using Claude.ai login rather than API-key auth.

## Storage

Forge auth storage gains separate Claude OAuth fields:

- access token
- refresh token
- expiry timestamp

These fields live alongside existing provider credentials in Forge auth storage and must never read from or write to external auth files.

## Provider Routing

`internal/bootstrap/runtime.go` gains a new `claude` provider backend:

- label: `Claude.ai subscription`
- status: `ready` when Claude OAuth is present, otherwise `sign in`
- default model: `claude/<default-claude-model>`

Model resolution rules:

- explicit `claude/<model>` uses the Claude OAuth driver
- explicit `anthropic/<model>` uses the Anthropic API-key driver
- unqualified Claude models prefer `claude` when Claude OAuth is ready
- otherwise they fall back to `anthropic` when an Anthropic API key is configured

Explicit prefixes always win.

## Driver

Forge adds a subscription-backed Claude driver or transport layer that:

- uses bearer auth from `claudeauth.Manager`
- refreshes tokens when needed
- applies any required Anthropic beta headers for the OAuth path
- preserves the existing LLM driver contract so runtime and session code remain unchanged

The existing API-key Claude driver remains in place for `anthropic/<model>`.

## TUI UX

The provider picker treats `claude` like other interactive-login providers:

- selecting `claude` in `sign in` state starts the auth flow
- Forge shows the auth URL and a short paste instruction
- after the user pastes the returned code or callback URL, Forge exchanges it and stores the session
- on success, Forge refreshes provider status and switches to the provider default model
- delete clears the Claude OAuth session and returns the provider to `sign in`

The provider copy should mention that `Claude` can sign in interactively, alongside `ChatGPT` and `Copilot`.

Because this mirrors unsupported OpenCode behavior, the Claude provider should be labeled as experimental or unsupported in the UI text.

## Errors

Claude login and refresh errors should be concise in chat and provider UI:

- `Claude sign-in failed`
- `Claude code exchange failed`
- `Claude session expired`
- `Paste the callback URL or authorization code`

Raw provider payloads should stay out of the main chat pane.

## Testing

Tests should cover:

- authorize URL creation and PKCE state in `internal/claudeauth`
- pasted raw code and pasted callback URL parsing
- exchange success and refresh success
- Forge-only session loading
- `claude` provider backend readiness and model routing
- TUI Claude sign-in, save, model switch, and delete behavior

## Risks

- This provider is unsupported by Anthropic and may break if Anthropic changes the flow
- OAuth-required headers or request shapes may drift over time
- Manual paste UX is less polished than a local callback, but simpler and more reliable
