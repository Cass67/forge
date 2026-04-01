# Forge Team First-Class Mode Design

## Summary

`forge team` should be the first-class live team mode in Forge.

The new mode should preserve the original creator/auditor "pong" idea, but rebuild it on Forge's modern host-owned runtime:

- live in-place execution against the current working tree
- shared approvals, tools, preview, and exec sessions
- typed runtime guidance and bounded retained context
- one shared user composer
- a split-pane UI showing creator and auditor activity side by side
- an optional verifier that runs at checkpoints rather than participating in every exchange

The same mode should also support a common bootstrap workflow:

1. create a new project directory
2. initialize a git repo if requested
3. scaffold the app
4. continue in the same workspace with the live team

This is a product-level addition of `forge team`.

## Goals

1. Promote `forge team` to a first-class Forge surface.
2. Make it feel as modern and reliable as the chat runtime.
3. Keep the original creator/auditor collaboration pattern visible.
4. Support both existing repos and brand-new project bootstraps.
5. Avoid reintroducing visible multi-agent chaos or an unbounded agent marketplace.

## Non-Goals

- do not build a general arbitrary-agent orchestration platform in this phase
- do not keep the old batch pipeline as the primary implementation under a new skin
- do not add per-agent user composers in v1
- do not add always-on third/fourth panes in v1

## Product Model

`forge team` becomes a dedicated live team mode.

It is distinct from normal `forge` chat, but it should reuse the same runtime-quality primitives wherever possible.

The mode has:

- one shared user composer
- one live workspace
- one visible creator pane
- one visible auditor pane
- optional verifier output at checkpoint moments

The user talks to the team as a whole. The host decides which team member should act next.

## Team Roles

### Creator

The creator is the builder.

Primary responsibilities:

- inspect the workspace
- scaffold code
- edit files
- run focused commands and previews
- respond to auditor critiques
- propose completion when work is actually ready

### Auditor

The auditor is the critical reviewer in the live loop.

Primary responsibilities:

- challenge weak claims
- request missing verification
- point out correctness, UX, security, or maintainability concerns
- block premature “done” states
- force tighter implementation or evidence before the team advances

The auditor should not be treated as a second creator. It is a constrained critic, not a parallel builder by default.

### Verifier

The verifier is not a full-time conversational peer.

It is invoked when:

- creator + auditor claim readiness
- the user asks for verification
- the host hits an explicit checkpoint such as “before done” or “before commit”

Primary responsibilities:

- run tests/checks
- inspect output quality
- report pass/fail with evidence
- return control to the creator/auditor loop if more work is needed

In v1, verifier output should appear as checkpoint/status output rather than requiring a permanent third pane.

## UX Model

### Shared Composer

The team uses one shared composer.

User messages are global team directives, not agent-specific instructions.

The host may still annotate or route those instructions internally, but the user should not have to think in terms of “message the creator” vs “message the auditor”.

### Split-Pane Layout

The live team UI should use the original side-by-side concept, built on the modern Bubble Tea chat shell.

Recommended layout:

- left pane: creator activity and outputs
- right pane: auditor critiques and checkpoint feedback
- shared footer/composer beneath both panes
- shared top chrome for model/provider/mode/status/approvals/runtime state

The panes should show structured activity, not just raw streaming buffers.

Examples:

- current step
- latest tool action
- latest key reasoning/result summary
- pending critique or unresolved blocker

### Modern Chrome To Reuse

The first-class `forge team` experience should reuse modern Forge UI/runtime affordances where possible:

- approvals
- runtime status
- exec-session visibility
- nudges and mode badges where appropriate
- recent activity / progress signals
- provider and model selection
- preview lifecycle visibility

## Runtime Architecture

## Core Principle

The primary engine for this mode should be a host-owned live runtime.

## Recommended Shape

Add a new team runtime layer that builds on modern chat/runtime primitives.

Likely shape:

- `internal/runtime/make.go`
  - team-mode assembly
  - workspace bootstrap
  - shared approvals/tools/runtime wiring
- `internal/team/`
  - creator/auditor/verifier team protocol
  - turn state
  - role routing
  - checkpoint policy
- reuse:
  - `internal/react`
  - `internal/hooks`
  - `internal/memory`
  - `internal/agent/tools`
  - `internal/tui`

The new runtime should own:

- active team state
- current workspace root
- current phase of work
- unresolved auditor blockers
- checkpoint/verifier status
- approval and command-session visibility

## Team Turn Protocol

The host should manage a structured loop like this:

1. user submits a shared directive
2. creator inspects/changes/runs tools
3. auditor critiques the creator’s progress or claimed result
4. if auditor finds issues, control returns to creator
5. if auditor is satisfied or a checkpoint is due, verifier runs
6. if verifier fails, control returns to creator with verifier evidence
7. if verifier passes, the team can surface completion or request user confirmation

This is a bounded team protocol, not free-form all-agent chatter.

## Workspace Model

### Existing Repo

If launched inside an existing repo, the team works directly in that repo, like chat mode does today.

### New Project Bootstrap

The team mode should also support bootstrapping a new workspace.

Example flow:

1. user runs `forge team` and says “create me a React app here”
2. Forge asks for or confirms target directory
3. Forge creates the directory
4. Forge optionally initializes git
5. creator scaffolds the project
6. team continues live inside that workspace

This should feel like one continuous session, not a bootstrap command followed by a separate mode switch.

## Tooling Model

The mode should reuse the modern tool registry, not the old write-artifact-only assumptions.

Expected tool families:

- file read/write/edit/apply-patch
- search/code-search/LSP
- git state and merge helpers
- preview tools
- exec sessions for long-running commands
- web tools where allowed
- plan/checkpoint helpers where useful

Role shaping should exist, but lightly:

- creator gets the full implementation-oriented toolset
- auditor gets mostly read/review/verification-oriented tools
- verifier gets validation/check/preview tools

The host should enforce this rather than relying only on prompt wording.

## Approval And Safety Model

Because this is a first-class mode, it must inherit the same approval quality as chat:

- guardian review
- approval gating
- structured tool visibility
- safe handling of long-running commands
- explicit interrupt behavior

The user should never have to accept weaker safety just because they chose team mode.

## Prompt And Memory Model

The team mode should reuse:

- typed hook overlays for runtime guidance
- bounded memory summaries
- task state / checkpoint state

But memory must be role-aware.

Examples:

- creator context should retain implementation-relevant progress
- auditor context should retain unresolved critique themes
- verifier context should retain latest validation state

In v1 this can still be implemented as one bounded host-owned memory summary with role-tagged entries, rather than separate long-lived memories per role.

## UI Architecture

The split-pane team mode should be built on the modern Bubble Tea shell.

Recommended approach:

- keep `ChatModel`-style shared chrome and footer behavior
- add a dedicated team-mode render path
- introduce structured per-role panes
- reuse approval overlays and command-session surfaces

Do not force pipeline-specific screens to become the main shell.

## Rollout Strategy

### Phase 1: Build The First-Class Team Runtime

- add `forge team` as a dedicated runtime surface
- validate the new experience with real repo and bootstrap flows

### Phase 2: Harden The Team Experience

- refine split-pane behavior
- tighten role routing and checkpoint behavior
- improve workspace bootstrap and preview ergonomics

## Risks

### Reusing The Old Pipeline Too Much

If the runtime is built on older batch assumptions instead of host-owned live orchestration, the mode will inherit the wrong constraints and never feel truly first-class.

### Too Much Agent Chatter

If creator/auditor/verifier become free-form conversational peers, the UI will become noisy and hard to steer.

The mitigation is a bounded host-owned team protocol.

### UI Complexity

A split-pane live team UI can become messy if it tries to expose every raw detail.

The mitigation is to show structured per-role activity and keep one shared composer and one shared runtime chrome.

### Workspace Bootstrap Edge Cases

Creating new repos/apps adds path, git-init, and preview/setup edge cases.

The mitigation is to make bootstrap an explicit host-owned phase rather than an improvised shell-script conversation.

## Recommendation

Build first-class `forge team` as a new live team mode on top of the modern runtime foundations, with:

- shared composer
- creator/auditor split-pane loop
- verifier checkpoints
- live in-place workspace execution
- first-class new-project bootstrap

That gives Forge a real “create/audit team” mode on a runtime designed for live collaboration.
