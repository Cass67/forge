import type { Workspace } from "../bridge";

function fmtWhen(iso: string): string {
  if (!iso) return "never";
  const d = new Date(iso);
  if (isNaN(d.getTime()) || d.getFullYear() < 2000) return "never";
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

// WorkspaceMenu lists the directories forge has threads for. Opening one
// launches a window rooted there, because the agent's tools, sandbox rules and
// thread store are all bound to the directory it started in.
export function WorkspaceMenu({
  workspaces,
  onOpen,
  onNewIn,
  onAdd,
  onClose,
}: {
  workspaces: Workspace[];
  onOpen: (dir: string) => void;
  // Double-click starts a fresh session in that workspace, switching to it
  // first when it is not the one already open.
  onNewIn: (dir: string) => void;
  onAdd: () => void;
  onClose: () => void;
}) {
  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal workspaces" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <span className="modal-badge">workspaces</span>
          <button className="btn" onClick={onAdd}>
            Open folder…
          </button>
          <button className="icon-btn close" onClick={onClose}>
            ✕
          </button>
        </div>
        <div className="ws-list">
          {workspaces.length === 0 ? (
            <div className="empty">no workspaces yet</div>
          ) : null}
          {workspaces.map((w) => (
            <button
              key={w.path}
              className={`ws-row ${w.active ? "active" : ""} ${w.missing ? "missing" : ""}`}
              // Keep current row mounted through second click so dblclick fires.
              onClick={() => {
                if (!w.active) onOpen(w.path);
              }}
              onDoubleClick={() => {
                onNewIn(w.path);
                onClose();
              }}
              disabled={w.missing}
              title={w.missing ? `${w.path} (no longer exists)` : w.path}
            >
              <span className="ws-row-name">{w.name}</span>
              <span className="ws-row-path">
                {w.path.replace(/^\/Users\/[^/]+/, "~")}
              </span>
              <span className="ws-row-meta">
                {w.threads} {w.threads === 1 ? "thread" : "threads"} ·{" "}
                {fmtWhen(w.last_use)}
              </span>
              {w.active ? <span className="ws-row-tag">current</span> : null}
              {w.missing ? (
                <span className="ws-row-tag warn">missing</span>
              ) : null}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
