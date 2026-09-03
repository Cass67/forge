# Agent Security Notes

Project-specific security guidance for coding agents. The repo-guard lockdown
rules in [AGENTS.md](./AGENTS.md) take precedence over anything here.

- Never read, print, summarize, diff, commit, or exfiltrate secrets from local files.
- Treat these paths and patterns as blocked unless the user explicitly asks for secret rotation or deletion: `.env`, `.env.*`, `*.pem`, `*.key`, `*.p12`, `*.pfx`, `id_rsa`, `id_dsa`, `id_ecdsa`, `id_ed25519`, `.ssh/`, `.gnupg/`, `auth.json`, and any config file containing API keys, client secrets, tokens, or private keys.
- When a task touches configuration, replace secret values with empty strings or placeholders. Do not preserve live credentials in repository files.
- Prefer environment variables or an external secret manager over repo-local config for credentials.
- If a secret is discovered in the workspace, redact it in-place when safe, avoid repeating the value in output, and advise the user to rotate it.
- Do not search `$HOME` broadly for secrets. Limit inspection to repository files needed for the task.
