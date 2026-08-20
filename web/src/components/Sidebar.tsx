import { useState } from "react";
import type { ThreadSummary, Workspace } from "../bridge";

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
  workspaces,
  workDir,
  activeID,
  busy,
  scoped,
  workspace,
  onNew,
  onRestore,
  onToggleScope,
  onAddWorkspace,
  onOpenWorkspace,
  onDelete,
}: {
  threads: ThreadSummary[];
  workspaces: Workspace[];
  workDir: string;
  activeID: string;
  busy: boolean;
  scoped: boolean;
  workspace: string;
  onNew: () => void;
  onRestore: (id: string) => void;
  onToggleScope: () => void;
  onAddWorkspace: () => void;
  onOpenWorkspace: (dir: string) => void;
  onDelete: (id: string) => void;
}) {
  const [showWorkspaces, setShowWorkspaces] = useState(false);
  const [confirming, setConfirming] = useState("");
  const current = workDir.split("/").pop() || "no workspace";
  const others = workspaces.filter((w) => !w.active);

  return (
    <aside className="sidebar">
      <div className="sidebar-head">
        <span className="section-label">Workspace</span>
        <button className="btn" onClick={onAddWorkspace} title="Open a folder to work in">
          Open…
        </button>
      </div>
      <button
        className={`ws-current ${showWorkspaces ? "open" : ""}`}
        onClick={() => setShowWorkspaces((v) => !v)}
        title={workDir}
      >
        <span className="ws-caret">{showWorkspaces ? "▾" : "▸"}</span>
        <span className="ws-current-name">{current}</span>
        <span className="ws-current-count">{others.length > 0 ? others.length : ""}</span>
      </button>
      {showWorkspaces ? (
        <div className="ws-recent">
          {others.length === 0 ? <div className="empty">no other workspaces yet</div> : null}
          {others.map((w) => (
            <button
              key={w.path}
              className={`ws-recent-row ${w.missing ? "missing" : ""}`}
              disabled={w.missing}
              onClick={() => onOpenWorkspace(w.path)}
              title={w.missing ? `${w.path} (no longer exists)` : w.path}
            >
              <span className="ws-recent-name">{w.name}</span>
              <span className="ws-recent-meta">{w.threads}</span>
            </button>
          ))}
        </div>
      ) : null}

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
        {threads.map((t) => {
          const active = t.thread_id === activeID;
          return (
            <div key={t.thread_id} className={`thread ${active ? "active" : ""}`}>
              <button
                className="thread-open"
                onClick={() => onRestore(t.thread_id)}
                title={t.preview || t.thread_id}
              >
                <span className="thread-title">{t.title || "Untitled"}</span>
                <span className="thread-meta">
                  <span className="thread-model">{t.model}</span>
                  <span>{fmtTime(t.updated_at)}</span>
                </span>
              </button>
              {confirming === t.thread_id ? (
                <span className="thread-confirm">
                  <button className="thread-x danger" onClick={() => onDelete(t.thread_id)} title="Delete permanently">
                    delete
                  </button>
                  <button className="thread-x" onClick={() => setConfirming("")}>
                    keep
                  </button>
                </span>
              ) : (
                <button
                  className="thread-x"
                  title={active ? "Cannot delete the thread you are in" : "Delete thread"}
                  disabled={active}
                  onClick={() => setConfirming(t.thread_id)}
                >
                  ✕
                </button>
              )}
            </div>
          );
        })}
      </div>
    </aside>
  );
}
