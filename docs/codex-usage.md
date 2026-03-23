# Codex Usage Integration

Forge now exposes two different kinds of usage in the chat UI:

- local per-session token totals gathered from Forge's own `EventStats`
- best-effort Codex ChatGPT allowance data shown in `/stats`

## What `/stats` shows

For OpenAI/Codex-backed chats, `/stats` now attempts a live lookup of Codex allowance windows:

- primary 5-hour window
- secondary 7-day window
- code review window when the backend returns it
- plan name when available

This is separate from OpenAI API token usage. The 5h/7d counters come from Codex's ChatGPT plan backend, not from the OpenAI API key path.

## Data source

Forge uses the same private Codex backend path described by third-party tools such as `codex-cli-usage`:

- local Codex login state from `CODEX_HOME/auth.json`, `~/.config/codex/auth.json`, or `~/.codex/auth.json`
- `GET https://chatgpt.com/backend-api/codex/usage`

If the cached access token is rejected, Forge attempts a refresh against:

- `POST https://auth.openai.com/oauth/token`

## Caveats

- This is not a documented public OpenAI Platform API.
- The schema and auth flow may change without notice.
- If Codex is not logged in locally, `/stats` will show an authentication error instead of live allowance data.
- Forge does not persist or display any secret token material; it only uses the local Codex auth file at runtime to make the request.

## Why this exists

OpenAI documents Codex usage in the ChatGPT/Codex UI, but does not document a supported public API for the same 5-hour and weekly counters. This integration is therefore intentionally labeled as a best-effort private-backend lookup.
