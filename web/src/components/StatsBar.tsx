import type { Stats } from "../sessionStats";
import type { ReactNode } from "react";

function fmt(n: number): string {
  return n.toLocaleString();
}

function fmtDur(ms: number): string {
  if (!ms) return "—";
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

// Output tokens over the wall time of the turn that produced them. Input
// tokens are excluded: they are submitted in one go, so counting them would
// inflate the rate rather than describe generation speed.
function fmtRate(outTokens: number, ms: number): string {
  if (!outTokens || ms <= 0) return "—";
  return `${(outTokens / (ms / 1000)).toFixed(1)}/s`;
}

export function StatsBar({
  stats,
  connected,
  actions,
}: {
  stats: Stats;
  connected: boolean;
  actions?: ReactNode;
}) {
  const total = stats.inTok + stats.outTok;
  const pct =
    stats.contextLimit > 0
      ? Math.round((stats.contextUsed / stats.contextLimit) * 100)
      : 0;
  return (
    <div className="stats-bar">
      <span className={`conn ${connected ? "on" : "off"}`}>
        <span className="conn-dot" />
        {connected ? "live" : "offline"}
      </span>
      <span className="stat">
        <span className="stat-k">model</span> {stats.model || "—"}
      </span>
      <span
        className="stat"
        title="Tokens sent to the model this session, cache hits included. Every tool step re-sends the whole conversation."
      >
        <span className="stat-k">in</span> {fmt(stats.inTok)}
        {stats.cachedTok > 0 ? (
          <span className="stat-sub"> ({fmt(stats.cachedTok)} cached)</span>
        ) : null}
      </span>
      <span className="stat" title="Tokens generated this session">
        <span className="stat-k">out</span> {fmt(stats.outTok)}
      </span>
      <span className="stat" title="Input plus output this session">
        <span className="stat-k">total</span> {fmt(total)}
      </span>
      <span className="stat" title="Output tokens per second on the last turn">
        <span className="stat-k">rate</span>{" "}
        {fmtRate(stats.lastOut, stats.lastMs)}
      </span>
      <span className="stat" title="Context used of the model's window">
        <span className="stat-k">ctx</span>{" "}
        {stats.contextLimit
          ? `${fmt(stats.contextUsed)}/${fmt(stats.contextLimit)} (${pct}%)`
          : "—"}
      </span>
      <span className="stat" title="Wall time of the last turn">
        <span className="stat-k">turn</span> {fmtDur(stats.durationMs)}
      </span>
      {actions ? <nav className="status-actions">{actions}</nav> : null}
    </div>
  );
}
