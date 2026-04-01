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
