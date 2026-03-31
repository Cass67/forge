# Forge Phase 2 Approval And Shell Rules Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Forge's approval system more reliable and predictable by adding structured shell-rule matching, explicit approval decision records, and clearer approval reasoning without changing Forge's overall approval model.

**Architecture:** Build on the existing `internal/react` approval gate rather than replacing it. Introduce a dedicated shell-rule matcher with exact/prefix/wildcard support, route config parsing through it, add an explicit approval decision/update record for auditability, and surface compact human-readable reasons from the approval gate. Keep the phase focused on runtime trust and determinism, not on new UI or hook features.

**Tech Stack:** Go, `internal/react`, `internal/config`, existing approval/guardian tests.

---

**Spec:** `docs/superpowers/specs/2026-03-31-forge-borrow-roadmap-design.md`

## File Structure

- Create: `internal/react/shell_rules.go`
  Responsibility: exact/prefix/wildcard rule parsing and matching for shell-like command strings.
- Create: `internal/react/shell_rules_test.go`
  Responsibility: focused matcher tests, including wildcard semantics and edge cases.
- Create: `internal/react/approval_updates.go`
  Responsibility: compact approval decision/update record types and formatting helpers.
- Create: `internal/react/approval_updates_test.go`
  Responsibility: tests for decision/update normalization and formatting.
- Modify: `internal/react/approval.go`
  Responsibility: replace ad hoc prefix matching with structured rules and surface approval reasons/updates.
- Modify: `internal/react/approval_config.go`
  Responsibility: parse config rules and known-safe entries into the new matcher model.
- Modify: `internal/react/approval_test.go`
  Responsibility: regression coverage for new rule types, reasons, and update records.
- Modify: `internal/react/approval_config_test.go`
  Responsibility: ensure config loading keeps behavior aligned with the new model.
- Modify: `internal/config/config.go`
  Responsibility: extend approval rule config to represent richer shell rules without breaking current configs.
- Modify: `internal/config/validate.go`
  Responsibility: validate the richer approval rule shape.
- Modify: `internal/config/config_test.go`
  Responsibility: cover config decode for richer approval rules.
- Modify: `internal/config/validate_test.go`
  Responsibility: cover validation failures for malformed approval rule configs.

## Task 1: Introduce Structured Shell Rule Matching

**Files:**
- Create: `internal/react/shell_rules.go`
- Create: `internal/react/shell_rules_test.go`
- Modify: `internal/react/approval.go`

- [ ] **Step 1: Write the failing matcher tests**

Add focused tests for:
- exact match
- token-prefix match
- wildcard match with `*`
- escaped wildcard handling
- mismatch behavior

Run: `go test ./internal/react -run 'TestShellRule'`
Expected: FAIL because the matcher types do not exist yet.

- [ ] **Step 2: Implement matcher types**

Create `internal/react/shell_rules.go` with:
- a parsed shell-rule type
- parsing for exact/prefix/wildcard forms
- matching helpers for command summary strings

Keep the matcher self-contained and deterministic.

- [ ] **Step 3: Replace ad hoc token-prefix logic in approvals**

Update `internal/react/approval.go` so:
- rule matching routes through the new matcher
- known-safe command checks also use the same matcher model

Do not change approval policy semantics yet beyond the matching engine.

- [ ] **Step 4: Verify the matcher slice**

Run: `go test ./internal/react -run 'TestShellRule|TestApprovalGateRule|TestApprovalGateUnlessTrusted'`
Expected: PASS

- [ ] **Step 5: Commit the task**

```bash
git add internal/react/shell_rules.go internal/react/shell_rules_test.go internal/react/approval.go internal/react/approval_test.go
git commit -m "react: add structured shell approval rules"
```

## Task 2: Add Approval Decision / Update Records

**Files:**
- Create: `internal/react/approval_updates.go`
- Create: `internal/react/approval_updates_test.go`
- Modify: `internal/react/approval.go`
- Modify: `internal/react/approval_test.go`

- [ ] **Step 1: Write the failing approval-update tests**

Add tests for:
- recording rule-based allow/prompt/forbid decisions
- recording sandbox-denied prompt escalation
- recording trusted-command auto-approval
- formatting human-readable reasons

Run: `go test ./internal/react -run 'TestApproval(Update|Decision)'`
Expected: FAIL because the new record types do not exist yet.

- [ ] **Step 2: Implement compact approval record types**

Create `internal/react/approval_updates.go` with:
- a small decision/update struct
- reason categories or source labels
- compact formatting helpers for tests and future UI/reporting

Keep it internal and data-only for now.

- [ ] **Step 3: Thread decision records through the approval gate**

Update `internal/react/approval.go` so the gate can:
- build a compact decision record for the chosen path
- attach or expose a clear human-readable reason

Do not overbuild persistence yet; phase 2 only needs explicit structured outcomes inside the runtime.

- [ ] **Step 4: Verify the decision-record slice**

Run: `go test ./internal/react -run 'TestApproval(Update|Decision)|TestApprovalGate'`
Expected: PASS

- [ ] **Step 5: Commit the task**

```bash
git add internal/react/approval_updates.go internal/react/approval_updates_test.go internal/react/approval.go internal/react/approval_test.go
git commit -m "react: record approval decisions explicitly"
```

## Task 3: Extend Config Shape And Validation

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/validate.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/validate_test.go`
- Modify: `internal/react/approval_config.go`
- Modify: `internal/react/approval_config_test.go`

- [ ] **Step 1: Write the failing config tests**

Add tests covering:
- current `command_prefix` compatibility
- richer exact/wildcard rule decoding
- invalid rule shapes rejected by validation

Run: `go test ./internal/config ./internal/react -run 'Test(LoadApprovalConfig|Config|Validate).*Approval'`
Expected: FAIL because the config shape does not yet support the richer rule model.

- [ ] **Step 2: Extend approval rule config**

Modify config types so approval rules can express the richer shell-rule form while keeping existing `command_prefix` configs valid.

Prefer additive config changes over breaking changes.

- [ ] **Step 3: Update validation and runtime loading**

Modify:
- `internal/config/validate.go`
- `internal/react/approval_config.go`

So malformed rules are rejected clearly and valid rules are normalized into the matcher model.

- [ ] **Step 4: Verify config compatibility**

Run: `go test ./internal/config ./internal/react -run 'Test(LoadApprovalConfig|Config|Validate).*Approval'`
Expected: PASS

- [ ] **Step 5: Commit the task**

```bash
git add internal/config/config.go internal/config/validate.go internal/config/config_test.go internal/config/validate_test.go internal/react/approval_config.go internal/react/approval_config_test.go
git commit -m "config: support richer approval shell rules"
```

## Task 4: Final Verification And Doc Check

**Files:**
- Modify any of the files above only if verification exposes a real issue

- [ ] **Step 1: Run the focused approval/config verification**

Run:
- `go test ./internal/react ./internal/config`

Expected: PASS

- [ ] **Step 2: Run the full repo verification**

Run:
- `go test ./...`
- `just check`

Expected: PASS

- [ ] **Step 3: Review the phase diff for accidental scope creep**

Run: `git diff --stat $(git merge-base HEAD main)..HEAD`
Expected: only the approval/config files above are included.

- [ ] **Step 4: Commit any final polish**

```bash
git add internal/react internal/config
git commit -m "chore: polish phase-2 approval rules"
```

## Notes For The Implementer

- Preserve existing approval policy semantics unless the plan explicitly changes them.
- Keep backward compatibility for existing `command_prefix` configs.
- Prefer deterministic rule matching over clever parsing.
- Do not add hook or UI behavior in this phase.
- If approval reasons need to be surfaced beyond tests, keep the interface small and internal so phase 3 can build on it without undoing phase 2.
