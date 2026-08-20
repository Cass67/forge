import type { Entry } from "../entries";
import type { Stats } from "./StatsBar";

// ActivityPanel is the right-hand column. MVP: a live context meter plus a
// compact feed of tool activity for the current session, derived from the
// transcript entries (no separate state to keep in sync).
export function ActivityPanel({ entries, stats }: { entries: Entry[]; stats: Stats }) {
  const tools = entries.filter((e) => e.t === "tool");
  const pct = stats.contextLimit > 0 ? Math.min(100, (stats.contextUsed / stats.contextLimit) * 100) : 0;
  return (
    <aside className="activity">
      <div className="col-head">context</div>
      <div className="ctx-meter">
        <div className="ctx-fill" style={{ width: `${pct}%` }} />
      </div>
      <div className="ctx-label">
        {stats.contextLimit ? `${stats.contextUsed.toLocaleString()} / ${stats.contextLimit.toLocaleString()} tokens` : "no context data"}
      </div>

      <div className="col-head act-head">activity</div>
      <div className="activity-list">
        {tools.length === 0 ? <div className="thread-empty">no tool activity yet</div> : null}
        {tools.map((t) => {
          if (t.t !== "tool") return null;
          const status = t.isError ? "err" : t.done ? "ok" : "run";
          return (
            <div key={t.id} className={`act ${status}`}>
              <span className="act-dot" />
              <span className="act-name">{t.name}</span>
            </div>
          );
        })}
      </div>
    </aside>
  );
}
