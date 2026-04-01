# Forge Phase 4 Memory Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden Forge's memory pipeline so retained memory is conservative, redacted, bounded, and prompt-friendly instead of behaving like a lightly filtered transcript cache.

**Architecture:** Keep the existing `internal/memory` package shape and tighten each stage in place: extraction decides whether a turn is memory-worthy, redaction strips secret-like material before retention, consolidation normalizes and bounds stored records, and pipeline orchestration stays thin. Preserve current runtime chat integration by continuing to emit a plain-text `MemorySummary`.

**Tech Stack:** Go, `internal/memory`, `internal/runtime`, `internal/react`, existing Go test suite.

---

**Spec:** `docs/superpowers/specs/2026-04-01-forge-phase-4-memory-hardening-design.md`

## File Structure

- Modify: `internal/memory/extract.go`
  Responsibility: gate unsafe or low-signal turns, normalize retained text, and bound extracted record content.
- Modify: `internal/memory/redact.go`
  Responsibility: expand secret-like pattern coverage while keeping redaction deterministic and local.
- Modify: `internal/memory/consolidate.go`
  Responsibility: normalize records, bound visible content, and produce compact deterministic summaries.
- Modify: `internal/memory/pipeline.go`
  Responsibility: keep orchestration thin while using the hardened extract/consolidate behavior.
- Modify: `internal/memory/pipeline_test.go`
  Responsibility: regression coverage for extraction, redaction, consolidation, and end-to-end pipeline behavior.

## Task 1: Redaction Coverage And Safe Extraction

**Files:**
- Modify: `internal/memory/extract.go`
- Modify: `internal/memory/redact.go`
- Modify: `internal/memory/pipeline_test.go`

- [ ] **Step 1: Write the failing tests**

Add tests covering:
- blocked snapshots do not produce retained memory
- errored turns do not produce retained memory
- extracted objective/summary text is trimmed and redacted
- obvious secret-like strings such as GitHub tokens, bearer tokens, and assignment-style secrets are redacted

Run: `go test ./internal/memory -run 'Test(ExtractSessionMemory|RedactText)'`
Expected: FAIL because extraction/redaction are still too permissive.

- [ ] **Step 2: Implement extraction gating and expanded redaction**

Update extraction so it:
- uses the latest meaningful successful turn
- skips blocked or errored turns
- trims normalized objective/summary text to compact limits

Update redaction so it covers additional obvious secret shapes while keeping replacements deterministic with `<REDACTED>`.

- [ ] **Step 3: Verify the slice**

Run: `go test ./internal/memory -run 'Test(ExtractSessionMemory|RedactText)'`
Expected: PASS

- [ ] **Step 4: Commit the task**

```bash
git add internal/memory/extract.go internal/memory/redact.go internal/memory/pipeline_test.go
git commit -m "memory: harden extraction and redaction"
```

## Task 2: Bounded Consolidation And Prompt-Friendly Summary Output

**Files:**
- Modify: `internal/memory/consolidate.go`
- Modify: `internal/memory/pipeline.go`
- Modify: `internal/memory/pipeline_test.go`

- [ ] **Step 1: Write the failing consolidation tests**

Add tests covering:
- record normalization and dedupe
- bounded newest-record retention
- compact summary bullets instead of raw large transcript spill
- deterministic ordering and fallback from summary to objective

Run: `go test ./internal/memory -run 'Test(ConsolidateRecords|Pipeline)'`
Expected: FAIL because consolidation still emits loosely bounded transcript-like summaries.

- [ ] **Step 2: Implement bounded consolidation**

Update consolidation so it:
- normalizes whitespace
- drops empty records
- dedupes normalized records
- retains the newest bounded set
- truncates per-record visible text to compact limits
- produces deterministic short bullet summaries

Keep `Pipeline.Process` thin and avoid widening the public surface.

- [ ] **Step 3: Verify the slice**

Run: `go test ./internal/memory -run 'Test(ConsolidateRecords|Pipeline)'`
Expected: PASS

- [ ] **Step 4: Commit the task**

```bash
git add internal/memory/consolidate.go internal/memory/pipeline.go internal/memory/pipeline_test.go
git commit -m "memory: bound retained summaries"
```

## Task 3: Final Verification And Runtime Integration Check

**Files:**
- Modify any of the files above only if verification exposes a real issue

- [ ] **Step 1: Run targeted package verification**

Run: `go test ./internal/memory ./internal/runtime ./internal/react`
Expected: PASS

- [ ] **Step 2: Run full repo verification**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: Run repo check command**

Run: `just check`
Expected: PASS

- [ ] **Step 4: Inspect the phase diff**

Run: `git diff --stat $(git merge-base HEAD main)..HEAD`
Expected: Only phase-4 docs and memory hardening changes.

- [ ] **Step 5: Commit any final verification-driven fixes**

```bash
git add <changed-files>
git commit -m "memory: polish phase 4 hardening"
```

Only do this if verification reveals a real issue that requires a code change.
