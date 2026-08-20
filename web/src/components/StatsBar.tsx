export type Stats = {
  inTok: number;
  outTok: number;
  contextUsed: number;
  contextLimit: number;
  durationMs: number;
  model?: string;
};

function fmt(n: number): string {
  return n.toLocaleString();
}

function fmtDur(ms: number): string {
  if (!ms) return "—";
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

export function StatsBar({ stats, connected }: { stats: Stats; connected: boolean }) {
  const total = stats.inTok + stats.outTok;
  return (
    <div className="stats-bar">
      <span className={`conn ${connected ? "on" : "off"}`}>
        <span className="conn-dot" />
        {connected ? "live" : "offline"}
      </span>
      <span className="stat">
        <span className="stat-k">model</span> {stats.model || "—"}
      </span>
      <span className="stat">
        <span className="stat-k">tokens</span> {fmt(total)}
      </span>
      <span className="stat">
        <span className="stat-k">ctx</span> {stats.contextLimit ? `${fmt(stats.contextUsed)}/${fmt(stats.contextLimit)}` : "—"}
      </span>
      <span className="stat">
        <span className="stat-k">turn</span> {fmtDur(stats.durationMs)}
      </span>
    </div>
  );
}
