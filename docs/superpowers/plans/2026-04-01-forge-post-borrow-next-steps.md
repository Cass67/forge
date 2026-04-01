# Forge Post-Borrow Next Steps

> Current state: the four-phase borrow roadmap is complete on `main`. This document captures what is still worth doing after that roadmap, separating true remaining reliability work from optional architecture polish.

## Summary

There is no unfinished required work from the March 31 borrow roadmap.

What remains falls into two buckets:

1. reliability work that is still high-value and should probably happen next
2. polish/cleanup work that improves coherence but is not blocking

The strongest next direction is still the Codex reliability gap plan. The rich-agent items are now mostly cleanup and consistency work rather than must-have borrow work.

## Next-Step Table

| Area | Type | Status | Suggested Priority | What remains | Why it still matters | Source |
| --- | --- | --- | --- | --- | --- | --- |
| Pending input as first-class runner state | Remaining | Not started | High | Move queued steering out of outer chat runtime ownership and into session/runner state; let the active workflow consume follow-up steering without handing control back to the outer loop. | This closes a real reliability gap where Forge can still feel stalled or lose momentum during active work. | `docs/superpowers/plans/2026-03-30-codex-reliability-gap-plan.md` |
| First-class exec sessions for long-running commands | Remaining | Not started | High | Replace synchronous shell execution with session-backed command handling, lifecycle events, and background status. | Long-running shell work remains one of the clearest ways Forge can still feel weaker than Codex. | `docs/superpowers/plans/2026-03-30-codex-reliability-gap-plan.md` |
| Early host-side turn routing | Remaining | Not started | High | Classify turns earlier and bias first actions/tool availability before the model freewrites or gets gated only at the end. | This should reduce wasted turns, fake progress, and tool-avoidant behavior. | `docs/superpowers/plans/2026-03-30-codex-reliability-gap-plan.md` |
| TUI runtime-state visibility | Remaining | Not started | Medium | Surface pending input, waiting states, approvals, background work, and interrupts more explicitly in the chat UI. | The runtime is stronger now, but the UI still hides too much state compared with Codex. | `docs/superpowers/plans/2026-03-30-codex-reliability-gap-plan.md` |
| Codex-style behavior evals | Remaining | Not started | Medium | Add transcript and package-level regressions for fake edits, fake verification, queued steering, preview lifecycle, and long-running command flows. | This turns known failure classes into locked regressions instead of relying on ad hoc manual checking. | `docs/superpowers/plans/2026-03-30-codex-reliability-gap-plan.md` |
| Short stabilization pass on new `main` | Suggested | Not formalized | High | Use the newly merged roadmap work for real tasks, collect friction, and fix only observed failures or UX pain. | The architecture work is broad enough that a soak pass is more efficient than inventing another large phase blindly. | Suggested from completed roadmap landing |
| Runtime guidance cleanup | Polish | Partly done | Medium | Re-check whether any remaining special-case runtime guidance should move to a more explicit structured surface rather than staying as compatibility plumbing. | Most transient coaching is already on overlays, so what remains is mainly consistency cleanup. | `docs/superpowers/plans/2026-03-31-forge-rich-agent-architecture.md` |
| Remove dead compatibility scaffolding | Polish | Partly done | Medium | Delete any transition helpers or compatibility shims left behind by the hook/runtime migration once proven unused. | This reduces maintenance cost and lowers the chance of duplicate guidance paths drifting apart. | `docs/superpowers/plans/2026-03-31-forge-rich-agent-architecture.md` |
| Refine runtime nudges and badges | Polish | Partly done | Low | Tune nudge priority, badge behavior, and stale-hint clearing based on real usage rather than architecture assumptions. | The nudge system exists now; polish is about making it feel intentional rather than noisy. | `docs/superpowers/plans/2026-03-31-forge-rich-agent-architecture.md` |
| Reassess host-vs-prompt state boundaries | Polish | Ongoing | Low | Continue collapsing prompt-shaped conventions into host-owned state where real defects justify it. | Good cleanup work, but lower leverage than the explicit reliability gaps above. | `docs/superpowers/plans/2026-03-31-forge-rich-agent-architecture.md` |

## Recommended Order

| Order | Track | Reason |
| --- | --- | --- |
| 1 | Stabilization pass | Cheapest way to catch real regressions from the completed roadmap work before starting another broad effort. |
| 2 | Early host-side turn routing | Highest leverage on everyday “wasted turn” failures. |
| 3 | Pending input as first-class runner state | Strong follow-on once routing is clearer; improves interruption and same-workflow continuation. |
| 4 | First-class exec sessions | Large reliability gain for long-running shell flows, but a little more invasive than routing/state work. |
| 5 | TUI runtime-state visibility | Best done after the runtime states above are explicit enough to surface cleanly. |
| 6 | Behavior eval expansion | Should grow alongside the work above, but becomes most valuable once the major runtime primitives land. |
| 7 | Rich-agent polish cleanup | Useful, but not the bottleneck while the reliability gaps remain open. |

## Practical Recommendation

If the goal is “make Forge better” rather than “keep porting ideas,” the next sensible move is:

1. short stabilization pass on the newly merged `main`
2. then execute the Codex reliability gap plan in slices, starting with early turn routing

If the goal is “make the architecture cleaner” and not necessarily more robust first, then the rich-agent cleanup items are the right polish track, but they should be treated as secondary to the reliability work above.

## Cleanup And Deprecation Table

The codebase still contains some legacy and compatibility surfaces. They are not all the same class of cleanup work.

| Surface | Current Reality | First-Class Status | Cleanup Recommendation | Notes |
| --- | --- | --- | --- | --- |
| `forge make` | Still has a public CLI entrypoint, README/ARCHITECTURE coverage, help text, tests, config wiring, prompts, and a full `internal/session` execution path. | Publicly supported, but explicitly legacy. | Do not remove as “dead code.” First decide whether to keep, deprecate, or replace it. If deprecating, do it in stages: docs note, warning, alias cleanup, then code removal. | This is a compatibility mode, not an abandoned leftover. |
| Auditor role/model | Still active inside the legacy pipeline: config has `models.auditor`, CLI flags expose `--auditor`, prompts exist, and the session runner depends on a writer/auditor round-trip. | First-class inside the legacy pipeline, not first-class in the modern chat/harness path. | Keep if `forge make` stays. If `forge make` is deprecated, auditor becomes a deprecation candidate with it. | The right question is product direction, not “can we delete it today?” |
| `forge improve` alias | Explicit compatibility alias for `forge make <path> [flags]`, still documented and help-tested. | Compatibility-only. | Good cleanup target once a deprecation decision is made. This is easier to remove than the whole pipeline. | Lowest-value public surface in this cluster. |
| Legacy `internal/session` pipeline | Still real code, with `runner.go`, `round.go`, tests, summarizer/audit-log generation, and artifact production. | Legacy runtime, not dead code. | Treat as a deliberate subsystem. Either keep and document it as supported legacy mode, or plan a formal retirement. | Removing it would be a product change, not janitorial cleanup. |
| Auditor prompts and audit-log artifacts | Still used by the legacy pipeline via `internal/prompts/*auditor*.md` and `audit-log.md` generation. | Active only because the legacy pipeline is active. | Remove only as part of a `forge make` retirement plan. | These are downstream cleanup items, not independent removal candidates. |
| Legacy compatibility wording in docs/help | Mixed: some places still promote the legacy path heavily even though chat is the primary mode. | Not code-critical. | Good immediate cleanup target. Reword docs/help to make chat primary and legacy mode clearly secondary. | Safe cleanup with low risk. |
| Old compatibility scaffolding from hook/runtime migration | Some likely remains in smaller helpers and bridging paths, though the major `RuntimeNote` cleanup already landed. | Transitional. | Good ongoing cleanup target during stabilization. Remove only after proving no behavior regressions. | This is the main “architecture polish” category. |

## Direct Answer: Is Auditor Still A First-Class Citizen?

Depends on which layer you mean:

- **As public product surface:** yes, but only inside the explicitly legacy `forge make` mode.
- **As strategic architecture direction:** no. The main investment is clearly the chat/harness/runtime path, not the writer/auditor pipeline.

So the accurate label today is:

**legacy but supported**, not dead and not the primary future path.

## Suggested Cleanup Order For Legacy Surfaces

| Order | Cleanup | Why |
| --- | --- | --- |
| 1 | Tone down docs/help promotion of legacy pipeline | Safest cleanup, clarifies product direction immediately. |
| 2 | Decide product policy for `forge make` | Needed before deleting aliases, auditor config, prompts, or session pipeline code. |
| 3 | If deprecating, remove `forge improve` first | Smallest public-surface cut with low architectural risk. |
| 4 | Deprecate/remove auditor-specific public config and flags | Only after `forge make` is on a retirement path. |
| 5 | Remove legacy session runner/prompt/artifact stack | Final cleanup step, only once the product decision is settled. |
