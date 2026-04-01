# Forge Phase 4 Memory Hardening Design

## Summary

Phase 4 hardens Forge's lightweight memory pipeline so it becomes conservative, bounded, and predictable instead of acting like a thin transcript cache.

The goal is to keep the existing `internal/memory` package shape while improving four responsibilities:

- extraction decides whether a turn is safe and useful to retain
- redaction removes secret-like material before storage
- consolidation keeps a compact bounded working set
- prompt injection surfaces only a short summary

This phase borrows Codex's discipline around bounded retained context and pairs it with Forge's local-first secret-handling posture.

## Current Problems

Forge already stores a memory summary between turns, but the current pipeline is too shallow to trust:

- `ExtractSessionMemory` falls back to the last assistant response almost verbatim.
- `RedactText` only catches two token families.
- `ConsolidateRecords` mostly appends, dedupes exact duplicates, and joins summaries into bullets.
- blocked or error-heavy turns can still produce retained summaries.

That is acceptable for experimentation, but not for a memory feature that should survive repeated use without leaking noisy or sensitive state back into prompts.

## Goals

1. Keep memory retention conservative.
2. Redact obvious secret-like values before they are stored or surfaced.
3. Keep the retained set bounded and deterministic.
4. Ensure prompt-visible memory stays short and useful.
5. Preserve Forge's current package boundaries and host-owned flow.

## Non-Goals

- do not build a long-term database or semantic memory system
- do not introduce embeddings, search, or ranking infrastructure
- do not retain raw transcripts or tool outputs as memory records
- do not attempt perfect secret detection in this phase

## Design

### 1. Harden Extraction

Extraction should only retain memory when the snapshot represents a useful, sufficiently successful turn.

The extractor should:

- prefer task objective or latest user intent as the memory objective
- derive summary text from the most recent successful assistant response
- skip turns that are likely poisoned or low-signal, including:
  - assistant responses missing a final response
  - turns with explicit turn errors
  - snapshots with active block output
  - responses that are mostly tool bookkeeping or empty formatting
- normalize whitespace and trim oversized objective/summary text before record creation

This keeps memory tied to meaningful turn outcomes instead of retaining accidental transcript debris.

### 2. Expand Redaction Conservatively

Redaction should remain regex-based, local, and deterministic, but cover more obvious secret-like material.

Add conservative patterns for common shapes such as:

- OpenAI-style keys
- AWS access key IDs
- GitHub tokens
- bearer-token style blobs
- common PEM/private-key markers
- generic long secret assignments such as `token=...`, `api_key=...`, `password=...`

The redactor should prefer false positives over false negatives for retained memory. Memory text is summarization context, not source-of-truth user data.

### 3. Make Consolidation Actually Summary-Oriented

Consolidation should stop acting like append-and-dedupe transcript storage.

Improve it by:

- normalizing records before dedupe
- keeping the newest bounded set
- truncating each record's visible objective/summary to a compact limit
- producing summary bullets that include enough context to be useful without copying entire responses

The summary format should remain plain text so `internal/react/prompt` does not need architectural changes.

### 4. Protect Prompt Injection

Prompt-visible memory should remain compact even if the retained record set hits its cap.

The summary builder should:

- omit empty records
- cap total line length per bullet
- prefer summary text, with objective as fallback
- keep ordering deterministic from oldest kept record to newest kept record

## Proposed Package Shape

Keep the current files but strengthen their responsibilities:

- `internal/memory/extract.go`
  - extraction gating
  - summary/objective normalization
- `internal/memory/redact.go`
  - redaction patterns and redaction helper
- `internal/memory/consolidate.go`
  - normalization, bounding, and final summary assembly
- `internal/memory/pipeline.go`
  - orchestration only

No new package is required for this phase.

## Testing Strategy

Add targeted tests for:

- extraction skipping blocked/error turns
- extraction trimming oversized retained text
- redaction of several obvious secret shapes
- consolidation dedupe and bounded retention
- summary assembly remaining short and deterministic
- pipeline integration preserving only safe retained context

## Acceptance Criteria

- memory records are only produced for useful successful turns
- secret-like text is redacted before entering `Record` or prompt summary state
- retained records remain bounded and deterministic
- prompt-visible memory is compact and does not spill large transcript fragments
- the existing runtime chat integration continues working with the hardened pipeline

## Risks

### Over-redaction

Conservative regexes may redact benign strings. That is acceptable here because memory is advisory context, not canonical storage.

### Under-detection

Regex redaction will still miss some secrets. The mitigation is to improve coverage for obvious families now and keep the pipeline bounded so the blast radius stays small.

### Over-filtering

If extraction becomes too strict, useful memory may disappear. The mitigation is targeted tests around the normal successful-turn path and modest, explainable gating rules.
