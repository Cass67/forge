import type { Entry } from "../entries";
import type { Stats } from "./StatsBar";
import { latestPlan, planProgress, type PlanStatus } from "../plan";

// Icons match the TUI's plan rendering, so the same plan reads the same way
// in either front end.
const PLAN_ICON: Record<PlanStatus, string> = {
  completed: "✓",
  in_progress: "◐",
  blocked: "⚠",
  failed: "✗",
  pending: "○",
};

export function ActivityPanel({
  entries,
  stats,
  subAgents = [],
}: {
  entries: Entry[];
  stats: Stats;
  // Sub-agent roles that have produced output this turn.
  subAgents?: string[];
}) {
  const pct =
    stats.contextLimit > 0
      ? Math.min(100, (stats.contextUsed / stats.contextLimit) * 100)
      : 0;
  const plan = latestPlan(entries);
  const progress = plan ? planProgress(plan) : null;
  return (
    <aside className="activity">
      <div className="col-head">context</div>
      <div className="ctx-meter">
        <div className="ctx-fill" style={{ width: `${pct}%` }} />
      </div>
      <div className="ctx-label">
        {stats.contextLimit
          ? `${stats.contextUsed.toLocaleString()} / ${stats.contextLimit.toLocaleString()} tokens`
          : "no context data"}
      </div>

      {plan && progress ? (
        <>
          <div className="col-head act-head">
            plan{" "}
            <span className="plan-count">
              {progress.done}/{progress.total}
            </span>
          </div>
          <ol className="plan-list">
            {plan.steps.map((step, i) => (
              <li
                className={`plan-step ${step.status}`}
                key={`${i}-${step.title}`}
              >
                <span className="plan-ic">{PLAN_ICON[step.status]}</span>
                <span
                  className="plan-title"
                  title={step.blocker ? `blocked: ${step.blocker}` : step.title}
                >
                  {step.title}
                </span>
              </li>
            ))}
          </ol>
        </>
      ) : null}

      {subAgents.length > 0 ? (
        <>
          <div className="col-head act-head">sub-agents</div>
          {subAgents.map((role) => (
            <div className="act run" key={role}>
              <span className="act-dot" />
              <span className="act-name">{role}</span>
            </div>
          ))}
        </>
      ) : null}
    </aside>
  );
}
