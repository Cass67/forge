# Harness Regression Hardening Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the harness against vague, typo-heavy repository review prompts by making evaluative reader tasks require grounded non-README evidence and by locking that behavior behind automated regression coverage.

**Architecture:** Keep the classifier semantic and typo-tolerant, but move the "README alone is not enough for evaluative repo reviews" rule into the Go control plane instead of English prompt phrasing. Propagate that requirement through `WorkerTask`, validate it in `contracts.go`, and reinforce it in worker context/retry prompts so the reader worker converges on grounded evidence instead of free-form summaries.

**Tech Stack:** Go, existing `internal/harness` runtime, Go test/build tooling.

---

## File Map

- Modify: `internal/harness/types.go`
  Purpose: add explicit control-plane flag for stricter reader evidence requirements.
- Modify: `internal/harness/runner.go`
  Purpose: set the stricter requirement for evaluative repository and directory inspect tasks and pass the right worker context.
- Modify: `internal/harness/contracts.go`
  Purpose: enforce non-README evidence grounding for applicable reader tasks.
- Modify: `internal/harness/workers.go`
  Purpose: make retry guidance reflect the stricter reader contract.
- Modify: `internal/harness/contracts_test.go`
  Purpose: cover non-README evidence validation.
- Modify: `internal/harness/workers_test.go`
  Purpose: cover retry behavior until a non-README file is actually read.
- Create: `internal/harness/repo_review_prompt_corpus_test.go`
  Purpose: corpus regression for typo-heavy repo review prompts.

## Chunk 1: Evaluative Reader Contract

### Task 1: Add the failing tests and corpus coverage

**Files:**
- Modify: `internal/harness/contracts_test.go`
- Modify: `internal/harness/workers_test.go`
- Create: `internal/harness/repo_review_prompt_corpus_test.go`

- [ ] **Step 1: Run the new focused tests to verify they fail**

Run: `go test ./internal/harness -run 'TestRepoReviewPromptCorpusRoutesToEvaluativeInspect|TestValidateWorkerResultWithToolCallsRejectsEvaluativeRepoReviewWithoutNonReadmeFileEvidence|TestManagerExecuteRetriesEvaluativeRepoReviewUntilNonReadmeFileIsRead' -count=1`
Expected: FAIL before implementation because the stricter evaluative-reader requirement is not wired through the control plane yet.

- [ ] **Step 2: Keep the new corpus broad and typo-heavy**

Cover prompts such as:
- `review this repo and suggest improvments`
- `take a look over this repo and point out any problms`
- `desribe this reposotory and tell me whats happeingin and what improvments could be made`

### Task 2: Implement the minimal control-plane fix

**Files:**
- Modify: `internal/harness/types.go`
- Modify: `internal/harness/runner.go`
- Modify: `internal/harness/contracts.go`
- Modify: `internal/harness/workers.go`

- [ ] **Step 1: Add a dedicated worker-task flag**

Add `RequireNonReadmeFileEvidence bool` to `WorkerTask`.

- [ ] **Step 2: Set that flag only for evaluative inspect tasks over repository or directory topics**

Use the classifier output and topic key in `runner.go` instead of hardcoded English output matching.

- [ ] **Step 3: Enforce the stricter grounding rule in `contracts.go`**

For complete reader results under that flag:
- README-only grounding is insufficient
- at least one grounded non-README file must be present
- command-only evidence or ungrounded file mentions remain invalid

- [ ] **Step 4: Tighten worker context and retry prompts**

Tell reader workers that evaluative walkthroughs must inspect at least one implementation, config, or entrypoint file beyond `README.md` before concluding.

### Task 3: Verify and land

**Files:**
- Modify: `internal/harness/*` as above

- [ ] **Step 1: Re-run the focused tests and confirm they pass**

Run: `go test ./internal/harness -run 'TestRepoReviewPromptCorpusRoutesToEvaluativeInspect|TestValidateWorkerResultWithToolCallsRejectsEvaluativeRepoReviewWithoutNonReadmeFileEvidence|TestManagerExecuteRetriesEvaluativeRepoReviewUntilNonReadmeFileIsRead' -count=1`
Expected: PASS

- [ ] **Step 2: Run broader verification**

Run:
- `go test ./internal/harness -count=1`
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`

- [ ] **Step 3: Commit directly to `main`**

Commit message target: `test: harden harness repo review regressions`
