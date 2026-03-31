# Forge Phase 1 Justfile Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a root `justfile` that becomes the canonical local workflow entrypoint for building, running, testing, and hook-compatible verification in Forge.

**Architecture:** Keep this phase small and repo-native. Add a thin `justfile` that wraps the commands Forge already documents, then update the main contributor docs to point at those recipes instead of repeating raw command lists as the primary interface. Verification should prove the recipes are discoverable and that the core build/test paths still work.

**Tech Stack:** Go, `just`, existing repo docs, existing `pre-commit` workflow.

---

**Spec:** `docs/superpowers/specs/2026-03-31-forge-borrow-roadmap-design.md`

## File Structure

- Create: `justfile`
  Responsibility: canonical repo task runner for common local workflows.
- Modify: `README.md`
  Responsibility: point user-facing quick-start and contributor-oriented command examples at `just`.
- Modify: `BUILD.md`
  Responsibility: make `just` the primary local build/test entrypoint while preserving raw Go commands as lower-level reference.
- Modify: `CONTRIBUTING.md`
  Responsibility: make contributor workflow and verification steps refer to `just` first.

## Task 1: Define The Recipe Surface

**Files:**
- Create: `justfile`
- Modify: `README.md`
- Modify: `BUILD.md`
- Modify: `CONTRIBUTING.md`

- [ ] **Step 1: Inventory the current canonical commands**

Read:
- `README.md`
- `BUILD.md`
- `CONTRIBUTING.md`

Capture the exact commands already promised to contributors, and reduce them to the smallest useful recipe set:
- `help`
- `build`
- `run`
- `test`
- `check`
- one targeted-test recipe
- one hook-compatible verification recipe

- [ ] **Step 2: Verify the missing-entrypoint baseline**

Run: `just --justfile justfile --list`
Expected: FAIL because `justfile` does not exist yet.

- [ ] **Step 3: Write the first `justfile` skeleton**

Create `justfile` with:
- a default help/list target
- `build` for `go build -o ./bin/forge ./cmd/forge`
- `run` for launching the built binary
- `test` for `go test ./...`
- `check` for `go build ./...` and `go test ./...`

Keep recipe names short and obvious. Do not introduce new behavior in phase 1.

- [ ] **Step 4: Verify recipe discovery**

Run: `just --justfile justfile --list`
Expected: PASS and list the initial recipes.

- [ ] **Step 5: Expand the `justfile` to the full phase-1 surface**

Add:
- one targeted-test recipe for package- or regex-scoped `go test`
- one hook-compatible verification recipe for `pre-commit run --all-files`

Keep `pre-commit` as an explicit recipe rather than making it implicit in `check`.

- [ ] **Step 6: Smoke-check non-destructive recipes**

Run:
- `just build`
- `just test`

Expected:
- `just build` succeeds and produces `./bin/forge`
- `just test` succeeds across the repo

- [ ] **Step 7: Commit the task**

```bash
git add justfile
git commit -m "build: add root justfile"
```

## Task 2: Repoint The Docs To Just

**Files:**
- Modify: `README.md`
- Modify: `BUILD.md`
- Modify: `CONTRIBUTING.md`

- [ ] **Step 1: Write the failing doc-consistency check**

Run: `rg -n "go build -o ./bin/forge ./cmd/forge|go test ./...|pre-commit run --all-files" README.md BUILD.md CONTRIBUTING.md`
Expected: PASS and show the current duplicated raw-command references that still need to be normalized around `just`.

- [ ] **Step 2: Update `README.md`**

Change the user-facing setup and local-run examples so:
- `just build` is the primary build example
- `just run` is the primary local launch example when appropriate
- raw `go build` remains available only as lower-level detail where it adds value

- [ ] **Step 3: Update `BUILD.md`**

Rewrite the primary local workflow sections so:
- `just build`, `just test`, and `just check` are the first-class commands
- raw `go build` / `go test` remain documented as the underlying equivalents

- [ ] **Step 4: Update `CONTRIBUTING.md`**

Rewrite contributor workflow and verification sections so:
- `just` is the first recommendation
- targeted test guidance names the targeted-test recipe if appropriate
- hook-compatible verification points at the explicit `pre-commit` recipe

- [ ] **Step 5: Verify docs reference the new workflow**

Run: `rg -n "just build|just test|just check|just pre-commit" README.md BUILD.md CONTRIBUTING.md`
Expected: PASS and show all three docs pointing at the `just` workflow.

- [ ] **Step 6: Commit the task**

```bash
git add README.md BUILD.md CONTRIBUTING.md
git commit -m "docs: document just-based workflow"
```

## Task 3: Final Verification And Cleanup

**Files:**
- Modify: `justfile` if recipe polish is needed
- Modify: `README.md` if wording drift is found
- Modify: `BUILD.md` if wording drift is found
- Modify: `CONTRIBUTING.md` if wording drift is found

- [ ] **Step 1: Run the final focused verification set**

Run:
- `just --list`
- `just build`
- `just test`
- `just check`

Expected:
- recipe list renders cleanly
- build succeeds
- tests succeed
- full check succeeds

- [ ] **Step 2: Run hook-compatible verification if local tooling is installed**

Run: `just pre-commit`
Expected: PASS if `pre-commit` and the required local tools are installed.

If local tooling is missing, capture that clearly in the implementation summary instead of silently skipping it.

- [ ] **Step 3: Review the final diff for accidental scope creep**

Run: `git diff --stat HEAD~2..HEAD`
Expected: only `justfile` plus the three workflow docs changed for this phase.

- [ ] **Step 4: Commit any final polish**

```bash
git add justfile README.md BUILD.md CONTRIBUTING.md
git commit -m "chore: polish phase-1 just workflow"
```

## Notes For The Implementer

- Do not add new product behavior in this phase.
- Do not turn `justfile` into a second build system; it should only wrap the commands Forge already endorses.
- Prefer explicit recipe names over clever parameterization.
- If `just` is not installed in the environment, note that in the execution summary, but still create the file and update the docs.
