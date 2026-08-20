import type { ThreadSummary } from "../bridge";

function fmtTime(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  return sameDay
    ? d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
    : d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export function Sidebar({
  threads,
  activeID,
  busy,
  scoped,
  workspace,
  onNew,
  onRestore,
  onToggleScope,
}: {
  threads: ThreadSummary[];
  activeID: string;
  busy: boolean;
  scoped: boolean;
  workspace: string;
  onNew: () => void;
  onRestore: (id: string) => void;
  onToggleScope: () => void;
}) {
  return (
    <aside className="sidebar">
      <div className="sidebar-head">
        <span className="section-label">Threads</span>
        <button className="btn" onClick={onNew} disabled={busy} title="New thread (⌘N)">
          + New
        </button>
      </div>
      <button
        className={`scope-toggle ${scoped ? "on" : ""}`}
        onClick={onToggleScope}
        title={scoped ? "Showing this workspace only" : "Showing every workspace"}
      >
        {scoped ? `▣ ${workspace || "this workspace"}` : "▢ all workspaces"}
      </button>
      <div className="thread-list">
        {threads.length === 0 ? (
          <div className="empty">{scoped ? "no threads in this workspace yet" : "no saved threads"}</div>
        ) : null}
        {threads.map((t) => (
          <button
            key={t.thread_id}
            className={`thread ${t.thread_id === activeID ? "active" : ""}`}
            onClick={() => onRestore(t.thread_id)}
            title={t.preview || t.thread_id}
          >
            <span className="thread-title">{t.title || "Untitled"}</span>
            <span className="thread-meta">
              <span className="thread-model">{t.model}</span>
              <span>{fmtTime(t.updated_at)}</span>
            </span>
          </button>
        ))}
      </div>
    </aside>
  );
}
