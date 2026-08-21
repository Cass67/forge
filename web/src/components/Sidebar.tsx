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
  onNew,
  onRestore,
  onAddWorkspace,
  onOpenWorkspace,
  onDelete,
  onRename,
  onPin,
  onForget,
  onBulkDelete,
}: {
  threads: ThreadSummary[];
  workspaces: Workspace[];
  workDir: string;
  activeID: string;
  busy: boolean;
  onNew: () => void;
  onRestore: (id: string) => void;
  onAddWorkspace: () => void;
  onOpenWorkspace: (dir: string) => void;
  onDelete: (id: string) => void;
  onRename: (id: string, title: string) => void;
  onPin: (dir: string, pinned: boolean) => void;
  onForget: (dir: string) => void;
  onBulkDelete: (threadIDs: string[], dirs: string[]) => void;
}) {
  const [confirming, setConfirming] = useState("");
  const [renaming, setRenaming] = useState("");
  const [closed, setClosed] = useState<Record<string, boolean>>({});
  const [selecting, setSelecting] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const activeWorkspace = workDir;

  const toggle = (key: string, on?: boolean) =>
    setPicked((current) => {
      const next = new Set(current);
      if (on ?? !next.has(key)) next.add(key);
      else next.delete(key);
      return next;
    });
  const toggleMany = (keys: string[], on: boolean) =>
    setPicked((current) => {
      const next = new Set(current);
      for (const key of keys) {
        if (on) next.add(key);
        else next.delete(key);
      }
      return next;
    });
  const leaveSelect = () => {
    setSelecting(false);
    setPicked(new Set());
  };
  const pickedThreads = [...picked]
    .filter((key) => key.startsWith("t:"))
    .map((key) => key.slice(2));
  const pickedDirs = [...picked]
    .filter((key) => key.startsWith("w:"))
    .map((key) => key.slice(2));

  // Every workspace that has been opened keeps its own section, so the list
  // grows as more are opened rather than replacing the one before.
  const sections = workspaces.length > 0 ? workspaces : [];

  return (
    <aside className="sidebar">
      <div className="sidebar-head">
        <span className="section-label">Workspaces</span>
        <button
          className={`btn ${selecting ? "primary" : ""}`}
          onClick={() => (selecting ? leaveSelect() : setSelecting(true))}
          title="Select several threads or workspaces and remove them at once"
        >
          {selecting ? "Done" : "Select"}
        </button>
        <button
          className="btn"
          onClick={onAddWorkspace}
          title="Open a folder to work in"
        >
          Open…
        </button>
      </div>
      <div className="ws-sections">
        {sections.map((ws) => {
          const isActive = ws.path === activeWorkspace;
          const collapsed = closed[ws.path];
          const list = threads.filter(
            (t) => (t.cwd ?? "") === ws.path || (isActive && !t.cwd),
          );
          return (
            <section
              className={`ws-section ${isActive ? "active" : ""}`}
              key={ws.path}
            >
              <div className="ws-section-head">
                {selecting ? (
                  <input
                    type="checkbox"
                    className="pick-box"
                    checked={picked.has(`w:${ws.path}`)}
                    disabled={isActive}
                    title={
                      isActive
                        ? "Cannot remove the workspace you are in"
                        : "Remove this workspace from the list"
                    }
                    onChange={(e) => toggle(`w:${ws.path}`, e.target.checked)}
                  />
                ) : null}
                <button
                  className="ws-section-title"
                  onClick={() =>
                    setClosed((c) => ({ ...c, [ws.path]: !c[ws.path] }))
                  }
                  onDoubleClick={() =>
                    isActive ? onNew() : onOpenWorkspace(ws.path)
                  }
                  title={ws.path}
                >
                  <span className="ws-caret">{collapsed ? "▸" : "▾"}</span>
                  <span className="ws-section-name">{ws.name}</span>
                  <span className="ws-section-count">{list.length || ""}</span>
                </button>
                <button
                  className={`ws-pin ${ws.pinned ? "on" : ""}`}
                  onClick={() => onPin(ws.path, !ws.pinned)}
                  title={
                    ws.pinned
                      ? "Pinned — click to unpin"
                      : "Pin so it always stays in the list"
                  }
                >
                  {ws.pinned ? "★" : "☆"}
                </button>
                {selecting ? (
                  <button
                    className="ws-pin"
                    title="Select every thread here that can be deleted"
                    onClick={() => {
                      const keys = list
                        .filter((t) => !(t.thread_id === activeID && isActive))
                        .map((t) => `t:${t.thread_id}`);
                      toggleMany(keys, !keys.every((key) => picked.has(key)));
                    }}
                  >
                    all
                  </button>
                ) : null}
                {isActive ? (
                  <button
                    className="btn"
                    onClick={onNew}
                    disabled={busy}
                    title="New thread (⌘N)"
                  >
                    +
                  </button>
                ) : (
                  <>
                    <button
                      className="btn"
                      onClick={() => onOpenWorkspace(ws.path)}
                      title={`Work in ${ws.path}`}
                    >
                      Open
                    </button>
                    <button
                      className="ws-pin"
                      onClick={() => onForget(ws.path)}
                      title="Remove from the list"
                    >
                      ✕
                    </button>
                  </>
                )}
              </div>
              {!collapsed ? (
                <div className="thread-list">
                  {list.length === 0 ? (
                    <div className="empty">no threads yet</div>
                  ) : null}
                  {list.map((t) => {
                    const current = t.thread_id === activeID && isActive;
                    return (
                      <div
                        key={t.thread_id}
                        className={`thread ${current ? "active" : ""} ${
                          picked.has(`t:${t.thread_id}`) ? "picked" : ""
                        }`}
                      >
                        {selecting ? (
                          <input
                            type="checkbox"
                            className="pick-box"
                            checked={picked.has(`t:${t.thread_id}`)}
                            disabled={current}
                            title={
                              current
                                ? "Cannot delete the thread you are in"
                                : "Select thread"
                            }
                            onChange={(e) =>
                              toggle(`t:${t.thread_id}`, e.target.checked)
                            }
                          />
                        ) : null}
                        {renaming === t.thread_id ? (
                          <input
                            className="thread-rename"
                            defaultValue={t.title || ""}
                            autoFocus
                            onFocus={(e) => e.currentTarget.select()}
                            onKeyDown={(e) => {
                              if (e.key === "Enter") {
                                const name = e.currentTarget.value.trim();
                                setRenaming("");
                                if (name && name !== t.title)
                                  onRename(t.thread_id, name);
                              } else if (e.key === "Escape") {
                                setRenaming("");
                              }
                            }}
                            onBlur={() => setRenaming("")}
                          />
                        ) : (
                          <button
                            className="thread-open"
                            onClick={() => {
                              if (selecting) {
                                if (!current) toggle(`t:${t.thread_id}`);
                                return;
                              }
                              if (isActive) onRestore(t.thread_id);
                              else onOpenWorkspace(ws.path);
                            }}
                            onContextMenu={(e) => {
                              e.preventDefault();
                              setRenaming(t.thread_id);
                            }}
                            title={t.preview || "Right-click to rename"}
                          >
                            <span className="thread-title">
                              {t.title || "Untitled"}
                            </span>
                            <span className="thread-meta">
                              <span className="thread-model">{t.model}</span>
                              <span>{fmtTime(t.updated_at)}</span>
                            </span>
                          </button>
                        )}
                        {selecting ? null : confirming === t.thread_id ? (
                          <span className="thread-confirm">
                            <button
                              className="thread-x danger"
                              onClick={() => onDelete(t.thread_id)}
                            >
                              delete
                            </button>
                            <button
                              className="thread-x"
                              onClick={() => setConfirming("")}
                            >
                              keep
                            </button>
                          </span>
                        ) : (
                          <button
                            className="thread-x"
                            title={
                              current
                                ? "Cannot delete the thread you are in"
                                : "Delete thread"
                            }
                            disabled={current}
                            onClick={() => setConfirming(t.thread_id)}
                          >
                            ✕
                          </button>
                        )}
                      </div>
                    );
                  })}
                </div>
              ) : null}
            </section>
          );
        })}
      </div>
      {selecting ? (
        <BulkBar
          threadCount={pickedThreads.length}
          dirCount={pickedDirs.length}
          onCancel={leaveSelect}
          onDelete={() => {
            onBulkDelete(pickedThreads, pickedDirs);
            leaveSelect();
          }}
        />
      ) : null}
    </aside>
  );
}

function BulkBar({
  threadCount,
  dirCount,
  onCancel,
  onDelete,
}: {
  threadCount: number;
  dirCount: number;
  onCancel: () => void;
  onDelete: () => void;
}) {
  const [confirm, setConfirm] = useState(false);
  const total = threadCount + dirCount;
  const parts = [
    threadCount ? `${threadCount} thread${threadCount === 1 ? "" : "s"}` : "",
    dirCount ? `${dirCount} workspace${dirCount === 1 ? "" : "s"}` : "",
  ].filter(Boolean);

  return (
    <div className="bulk-bar">
      <span className="bulk-count">
        {total === 0 ? "nothing selected" : parts.join(" + ")}
      </span>
      {confirm ? (
        <>
          <button className="btn danger" onClick={onDelete}>
            Confirm
          </button>
          <button className="btn" onClick={() => setConfirm(false)}>
            Keep
          </button>
        </>
      ) : (
        <>
          <button
            className="btn danger"
            disabled={total === 0}
            onClick={() => setConfirm(true)}
          >
            Remove
          </button>
          <button className="btn" onClick={onCancel}>
            Cancel
          </button>
        </>
      )}
    </div>
  );
}
