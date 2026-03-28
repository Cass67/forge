# Harness Preview And Artifact Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn preview work into a host-owned lifecycle with tracked sessions, render validation, reuse, repair, and teardown so visible collaboration can prove that a preview is not only present but actually usable.

**Architecture:** Extend the current artifact and preview tools into a real preview subsystem. The host should track preview sessions separately from generic session memory, validate served artifacts before announcing success, and support replay, repair, and supersession without rediscovery or shell-authored process management.

**Tech Stack:** Go, `internal/agent/tools`, `internal/harness`, `internal/runtime`, Go test/build tooling, preview fixtures.

---

## References

- `docs/superpowers/specs/2026-03-27-harness-production-redesign-design.md`
- `internal/agent/tools/artifact.go`
- `internal/agent/tools/preview.go`
- `internal/harness/session.go`
- `internal/runtime/chat.go`

## File Map

- Create: `internal/agent/tools/preview_probe.go`
  Purpose: fetch and classify renderability for tracked preview targets.
- Create: `internal/agent/tools/preview_probe_test.go`
- Modify: `internal/agent/tools/artifact.go`
- Modify: `internal/agent/tools/preview.go`
- Modify: `internal/agent/tools/registry.go`
- Modify: `internal/harness/types.go`
- Modify: `internal/harness/session.go`
- Modify: `internal/harness/runner.go`
- Modify: `internal/runtime/chat.go`
- Modify: tests under `internal/agent/tools`, `internal/harness`, and `internal/runtime`

## Task 1: Add Failing Preview-Lifecycle Tests

- [ ] **Step 1: Write failing tests**

Cover:

- a preview session carries a stable identifier across turns
- host reuse of an existing preview is explicit, not accidental
- render probing marks escaped HTML or otherwise non-renderable content as invalid
- preview teardown or supersession clears the active preview session cleanly

- [ ] **Step 2: Run focused tests and confirm failure**

Run:

- `go test ./internal/agent/tools -run 'Test(Artifact|Preview|PreviewProbe)' -count=1`
- `go test ./internal/harness -run 'Test(Session|Runner).*(Preview|Artifact|Render)' -count=1`
- `go test ./internal/runtime -run 'TestRunChat.*Preview' -count=1`

Expected: FAIL because the current preview model does not yet encode session-level lifecycle and render validation.

## Task 2: Add Preview Session And Render Validation

- [ ] **Step 1: Extend preview state**

Track:

- preview session id
- artifact handle
- live route and URL
- last probe time
- last probe result
- render status

- [ ] **Step 2: Add a host-owned render probe**

Require the host to detect at least:

- served artifact reachable
- expected MIME/content class
- likely entity-escaped full-document HTML
- empty or obviously invalid output

- [ ] **Step 3: Tie preview completion to render validation**

Do not announce a successful preview when:

- the server is live but the served content is not renderable
- the artifact was written but not published
- the preview session was superseded or stopped

## Task 3: Add Reuse, Repair, And Teardown Rules

- [ ] **Step 1: Reuse preview sessions explicitly**

Prefer:

- reusing the active preview session when the artifact and route still match the active thread
- superseding the preview session when the artifact meaningfully changes

- [ ] **Step 2: Close preview sessions explicitly**

On:

- chat teardown
- thread cancellation
- thread supersession
- explicit preview stop requests

## Task 4: Verification

- [ ] **Step 1: Re-run focused preview tests**

Run the three commands above and confirm PASS.

- [ ] **Step 2: Run affected packages**

Run:

- `go test ./internal/agent/tools ./internal/harness ./internal/runtime -count=1`

Expected: PASS.

- [ ] **Step 3: Build Forge**

Run:

- `go build -o ./forge ./cmd/forge`

Expected: exit `0`.
