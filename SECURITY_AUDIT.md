# Security Audit

Audit target: `/Users/cass/git/forge`  
Date: 2026-05-07

## Summary

This repository handles sensitive credentials, OAuth tokens, shell execution, MCP servers, plugins, sandbox policy, and LLM provider integrations. The highest-risk areas are local secret storage, configurable command execution, plugin/MCP environment inheritance, and user-configurable sandbox weakening.

No evidence of hardcoded private secrets was identified from the inspected context. Several areas should be hardened or documented more explicitly because compromise of config files, plugins, or MCP definitions could expose provider credentials or execute arbitrary commands.

## Findings

### 1. OAuth/API tokens are stored unencrypted on disk

**Severity:** Medium  
**Affected files:**

- `internal/auth/store.go`
- `internal/mcp/auth_store.go`

**Description:**

The repository stores provider credentials and OAuth tokens in local JSON files such as `auth.json` and `mcp_tokens.json`. These can include provider API keys, ChatGPT OAuth tokens, Claude OAuth tokens, MCP bearer tokens, and custom provider credentials.

The files appear to be written with restrictive permissions (`0600`) and config directories with `0700`, which is good baseline protection. However, credentials are still stored in plaintext.

**Impact:**

An attacker with access to the user’s Forge config directory could steal provider API keys and OAuth refresh tokens, potentially allowing account access, billing abuse, or data exfiltration.

**Recommendation:**

Consider optional OS-backed secret storage such as macOS Keychain, Linux Secret Service/libsecret, or Windows Credential Manager. If plaintext fallback remains, document the risk clearly and warn when file permissions are weaker than expected.

---

### 2. Plugin and MCP configuration can execute arbitrary local commands

**Severity:** High  
**Affected files:**

- `internal/config/config.go`
- `internal/config/validate.go`
- `internal/plugins/manager.go`
- `internal/plugins/client.go`
- `internal/plugins/opencode_host.go`
- `internal/mcp/manager.go`

**Description:**

The configuration supports user-defined plugin and MCP server commands through fields such as `PluginConfig.Command`, `MCPServerConfig.Command`, `Env`, `InheritEnv`, headers, URLs, and auto-approval settings.

Validation appears to check structural correctness, such as plugin IDs and environment variable names, but arbitrary configured commands remain inherently dangerous.

**Impact:**

A malicious or compromised plugin/MCP configuration could execute arbitrary commands, read project files, steal API keys, exfiltrate source code, or persist by modifying local configuration.

**Recommendation:**

Treat command-backed plugins and MCP servers as trusted code execution. Require explicit approval before enabling new commands, display command/env/source details before first run, invalidate trust when command or source changes, and document the risk clearly.

---

### 3. Environment inheritance may expose secrets to plugins

**Severity:** Medium to High  
**Affected files:**

- `internal/config/config.go`
- `internal/plugins/manager.go`
- `internal/mcp/manager.go`

**Description:**

Plugin and MCP configurations can include explicit environment variables and may inherit the parent process environment. If inherited broadly, plugins may receive secrets such as provider API keys, GitHub tokens, cloud credentials, SSH agent paths, database credentials, and CI/CD tokens.

**Impact:**

A plugin or MCP server could exfiltrate secrets available in the environment.

**Recommendation:**

Use a deny-by-default environment model for plugins and MCP servers. Default to no inherited environment where possible, support explicit allowlists, and warn when passing variables matching sensitive patterns such as `*_TOKEN`, `*_SECRET`, `*_KEY`, `*_PASSWORD`, `AWS_*`, `GITHUB_*`, `OPENAI_*`, or `ANTHROPIC_*`.

---

### 4. Auto-approved plugin tools can bypass meaningful user review

**Severity:** Medium  
**Affected files:**

- `internal/config/config.go`
- `internal/react/approval.go`
- `internal/react/approval_config.go`
- `internal/react/approval_updates.go`
- `internal/plugins/*`

**Description:**

Plugin configuration includes auto-approval behavior for tools. If plugin tools can be auto-approved too broadly, malicious or compromised plugins may perform sensitive actions without meaningful user confirmation.

**Impact:**

A malicious plugin could read files, modify files, run commands, send data externally, or chain tool calls unexpectedly.

**Recommendation:**

Require explicit opt-in per plugin and per tool, show clear warnings before enabling auto-approval, bind approvals to plugin identity/source/version, invalidate approvals when plugin command/source changes, and provide a way to list and revoke auto-approved tools.

---

### 5. `danger_full_access` sandbox mode is supported

**Severity:** Medium  
**Affected files:**

- `internal/config/validate.go`
- `internal/react/sandbox.go`
- `internal/react/approval_config.go`

**Description:**

The configuration supports sandbox policies including `read_only`, `workspace_write`, and `danger_full_access`. Full access may be needed for some workflows, but it is a significant security risk if enabled accidentally or inherited from shared/untrusted config.

**Impact:**

When enabled, model-suggested or tool-executed actions may have broad access to the user’s system, increasing risk from prompt injection, malicious repository content, or compromised plugins.

**Recommendation:**

Require explicit confirmation before enabling `danger_full_access`, avoid making it the default, visually indicate when active, document the risk, and consider refusing to load it from untrusted workspace config without confirmation.

---

### 6. Configurable MCP headers may contain plaintext bearer tokens

**Severity:** Medium  
**Affected files:**

- `internal/config/config.go`
- `internal/mcp/auth_store.go`

**Description:**

MCP configuration supports custom headers and also stores MCP bearer tokens. If users place bearer tokens directly in config headers, secrets may be stored in plaintext config files and may be accidentally committed or logged.

**Impact:**

MCP credentials could leak through checked-in config, logs, debug output, support bundles, or local compromise.

**Recommendation:**

Prefer secret references or the dedicated MCP token store instead of inline header values. Warn when header names include sensitive words such as `Authorization`, `Token`, `Key`, or `Secret`, and ensure configured headers are redacted from logs.

---

### 7. OAuth implementations should maintain strict redirect and state handling

**Severity:** Low to Medium  
**Affected files:**

- `internal/claudeauth/auth.go`
- `internal/chatgptauth/auth.go`

**Description:**

The Claude OAuth implementation appears to use PKCE and validate callback state. ChatGPT/Codex auth uses OAuth/device-style flows and token refresh logic. These flows are security-sensitive and should have tests preventing regressions.

**Impact:**

A regression in state validation, PKCE handling, redirect validation, or token refresh behavior could allow auth-flow interference or token exposure.

**Recommendation:**

Add or maintain tests for state mismatch rejection, missing state rejection, PKCE verifier/challenge generation, token refresh failure handling, expired token behavior, token redaction in logs/errors, and expected redirect restrictions.

---

### 8. Sensitive values should be consistently redacted from logs and errors

**Severity:** Medium  
**Affected files:**

- `internal/auth/store.go`
- `internal/mcp/auth_store.go`
- `internal/llm/drivers/*`
- `internal/plugins/*`
- `internal/mcp/*`

**Description:**

The repository handles many secret-bearing values, including authorization headers, API keys, OAuth tokens, MCP bearer tokens, and plugin/MCP environment variables. Logging request headers, config structs, environment maps, or wrapped errors could leak secrets.

**Impact:**

Secrets may be exposed through terminal output, logs, bug reports, telemetry, or CI logs.

**Recommendation:**

Add centralized redaction helpers for HTTP headers, config structs, environment maps, token stores, and provider request/response errors. Redact values whose keys contain terms such as `authorization`, `token`, `secret`, `password`, `apikey`, `api_key`, or `key`.

## Positive Observations

- Token files are written with restrictive permissions (`0600`).
- Config directories are created with restrictive permissions (`0700`).
- Claude OAuth appears to use PKCE.
- Claude OAuth validates callback `state`.
- Config validation exists for plugin IDs, environment variable names, approval policy values, sandbox policy values, and shell rule shape.
- Approval and sandboxing are explicit concepts in the codebase.

## Recommended Next Steps

1. Add OS-backed secret storage for OAuth refresh tokens and API keys.
2. Tighten plugin/MCP environment inheritance defaults.
3. Add warnings and trust prompts for command-backed plugins/MCP servers.
4. Ensure auto-approved tools are scoped to plugin identity/source/version.
5. Add centralized secret redaction utilities.
6. Add tests around OAuth state/PKCE/token handling.
7. Document the security model for plugins, MCP servers, sandbox modes, and local secret storage.

## Notes

This audit was based on repository inspection context for security-relevant areas. A deeper audit should include line-by-line review of shell command execution, sandbox enforcement, plugin process spawning, MCP transport handling, HTTP logging/retry code, and CI/dependency supply-chain configuration.
