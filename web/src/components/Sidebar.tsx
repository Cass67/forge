import { useEffect, useMemo, useState } from "react";
import type { ThreadSummary, Workspace } from "../bridge";
import type { Attention } from "../attention";
import {
  labelFor,
  loadThreadOrders,
  ordered,
  saveThreadOrder,
  searchSections,
  splitSections,
  workspacesWithThreads,
} from "../sidebarSections";
import type { SessionStatus } from "../sessionTabs";
import { NameDialog } from "./FileDialog";

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
  busyWorkspaces = {},
  terminalWorkspaces = {},
  onNew,
  onRestore,
  onOpenThread,
  onAddWorkspace,
  onOpenWorkspace,
  onNewIn,
  onDelete,
  onRename,
  onPin,
  onForget,
  onBrowse,
  browsing = "",
  onBulkDelete,
  onClearThreads,
  onPickWorkspaceParent,
  onCreateWorkspace,
  threadStatus = {},
  threadAttention = {},
}: {
  threads: ThreadSummary[];
  workspaces: Workspace[];
  workDir: string;
  activeID: string;
  busy: boolean;
  // Workspaces with an agent turn in flight in their own runtime, keyed by
  // directory. Shown as a liveness dot on each section.
  busyWorkspaces?: Record<string, boolean>;
  // Workspaces with at least one open terminal pane.
  terminalWorkspaces?: Record<string, boolean>;
  onNew: () => void;
  onRestore: (id: string) => void;
  onOpenThread: (id: string) => void;
  onAddWorkspace: () => void;
  onOpenWorkspace: (dir: string) => void;
  onNewIn: (dir: string) => void;
  onDelete: (id: string) => void;
  onRename: (id: string, title: string) => void;
  onPin: (dir: string, pinned: boolean) => void;
  onForget: (dir: string) => void;
  // Point the file tree, editor and source control at a workspace without
  // starting a chat in it.
  onBrowse: (dir: string) => void;
  // The workspace those panels are currently showing.
  browsing?: string;
  onBulkDelete: (threadIDs: string[], dirs: string[]) => void;
  onClearThreads: () => void;
  // "Create folder" flow: pick where (via the OS dialog) then name it.
  onPickWorkspaceParent: () => Promise<string | null>;
  onCreateWorkspace: (parent: string, name: string) => Promise<void>;
  // Latest known status per session, for the running/waiting/stopped dot.
  threadStatus?: Record<string, SessionStatus>;
  threadAttention?: Record<string, Attention>;
}) {
  const [orders, setOrders] = useState<Record<string, string[]>>(() =>
    loadThreadOrders(),
  );
  const [dragID, setDragID] = useState("");
  const [renaming, setRenaming] = useState("");
  const [closed, setClosed] = useState<Record<string, boolean>>({});
  const [query, setQuery] = useState("");
  const [searchIndex, setSearchIndex] = useState(0);
  const [selecting, setSelecting] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  // The parent directory chosen by the OS picker while the create-folder dialog
  // is open, or "" when no pick has happened yet.
  const [pendingParent, setPendingParent] = useState("");
  const activeWorkspace = workDir;
  const visibleWorkspaces = useMemo(
    () => workspacesWithThreads(workspaces, threads, activeWorkspace),
    [activeWorkspace, threads, workspaces],
  );

  // "Create folder" is two steps: an OS picker for where, then a name dialog
  // once a parent directory has been chosen. The props resolve the happy path;
  // App owns error reporting, so failures surface there.
  const startCreateFolder = () => {
    setPendingParent("");
    void onPickWorkspaceParent().then((parent) => {
      if (parent) setPendingParent(parent);
    });
  };
  const confirmCreateFolder = (name: string) => {
    const parent = pendingParent;
    setPendingParent("");
    void onCreateWorkspace(parent, name);
  };

  const collapseAllWorkspaces = () => {
    setClosed(
      Object.fromEntries(visibleWorkspaces.map((ws) => [ws.path, true])),
    );
  };

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

  // Everything Remove is allowed to touch: every workspace except the one the
  // chat is in, and every chat except the one on screen.
  const selectableKeys = [
    ...workspaces
      .filter((ws) => ws.path !== activeWorkspace)
      .map((ws) => `w:${ws.path}`),
    ...threads
      .filter((t) => t.thread_id !== activeID)
      .map((t) => `t:${t.thread_id}`),
  ];
  const selectableCount = selectableKeys.length;
  const selectEverything = () =>
    toggleMany(selectableKeys, !selectableKeys.every((key) => picked.has(key)));

  // A section exists per directory any stored thread ran in, which on a
  // machine that has done benchmark runs means a hundred of them, most holding
  // one thread and most called "work". Names are grown until they differ, and
  // everything past the first few sits behind one toggle.
  const labels = labelFor(visibleWorkspaces.map((ws) => ws.path));
  // Threads grouped by workspace, in one pass. Filtering the whole list per
  // section meant every render cost sections x threads, which on a machine
  // with a folder of repositories open is the sidebar's whole budget.
  const byWorkspace = useMemo(() => {
    const groups = new Map<string, ThreadSummary[]>();
    for (const thread of threads) {
      // The active workspace also owns threads with no recorded directory,
      // which is what a conversation looks like before its first message.
      const key = thread.cwd ? thread.cwd : activeWorkspace;
      const group = groups.get(key);
      if (group) group.push(thread);
      else groups.set(key, [thread]);
    }
    for (const [path, group] of groups) {
      groups.set(path, ordered(group, orders[path] ?? []));
    }
    return groups;
  }, [activeWorkspace, orders, threads]);
  const threadsIn = (path: string) => byWorkspace.get(path) ?? [];
  // While searching, the list is exactly the matches: a query is a request to
  // see one project, not to highlight it among ninety others.
  const matches = searchSections(visibleWorkspaces, threadsIn, query);
  const searching = query.trim() !== "";
  const { shown: unfiltered, hidden } = splitSections(
    visibleWorkspaces,
    showAll,
  );
  const sections = searching ? matches.map((m) => m.workspace) : unfiltered;
  const matchedThreads = new Map(
    matches.map((m) => [m.workspace.path, m.threads]),
  );
  const searchResults = matches.flatMap((match) => match.threads);
  useEffect(() => setSearchIndex(0), [query]);
  const selectedSearchID = searching
    ? searchResults[Math.min(searchIndex, searchResults.length - 1)]?.thread_id
    : undefined;

  return (
    <aside className="sidebar">
      <div className="sidebar-head">
        <span className="section-label">Workspaces</span>
        <button
          className="btn"
          onClick={collapseAllWorkspaces}
          title="Collapse every workspace section"
        >
          Collapse all
        </button>
        <button
          className={`btn ${selecting ? "primary" : ""}`}
          onClick={() => (selecting ? leaveSelect() : setSelecting(true))}
          title="Manage several chats or workspaces at once"
        >
          {selecting ? "Done" : "Manage"}
        </button>
      </div>
      <div className="ws-search">
        <input
          aria-label="Search workspaces"
          className="ws-search-input"
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing) return;
            if (event.key === "Escape") setQuery("");
            else if (event.key === "ArrowDown" && searchResults.length) {
              event.preventDefault();
              setSearchIndex((current) =>
                Math.min(current + 1, searchResults.length - 1),
              );
            } else if (event.key === "ArrowUp" && searchResults.length) {
              event.preventDefault();
              setSearchIndex((current) => Math.max(current - 1, 0));
            } else if (event.key === "Enter" && selectedSearchID) {
              event.preventDefault();
              onOpenThread(selectedSearchID);
            }
          }}
          aria-activedescendant={
            selectedSearchID ? `thread-search-${selectedSearchID}` : undefined
          }
          placeholder="Search folders and chats"
          value={query}
        />
        {searching ? (
          <button
            className="ws-pin"
            onClick={() => setQuery("")}
            title="Clear the search"
          >
            ✕
          </button>
        ) : null}
      </div>
      <div className="ws-sections">
        {sections.map((ws) => {
          const isActive = ws.path === activeWorkspace;
          const collapsed = closed[ws.path];
          const list = searching
            ? (matchedThreads.get(ws.path) ?? [])
            : threadsIn(ws.path);
          const dropBefore = (targetID: string) => {
            if (!dragID || dragID === targetID) return;
            const ids = list.map((t) => t.thread_id);
            const from = ids.indexOf(dragID);
            const to = ids.indexOf(targetID);
            if (from < 0 || to < 0) return;
            ids.splice(to, 0, ids.splice(from, 1)[0]);
            saveThreadOrder(ws.path, ids);
            setOrders((current) => ({ ...current, [ws.path]: ids }));
            setDragID("");
          };
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
                  aria-label={collapsed ? "Expand" : "Collapse"}
                  className="ws-chevron"
                  onClick={() =>
                    setClosed((c) => ({ ...c, [ws.path]: !c[ws.path] }))
                  }
                  title={collapsed ? "Show chats" : "Hide chats"}
                >
                  {collapsed ? "›" : "⌄"}
                </button>
                <button
                  className={`ws-section-title ${ws.path === browsing ? "browsing" : ""}`}
                  onClick={() => onBrowse(ws.path)}
                  onDoubleClick={() => onNewIn(ws.path)}
                  title={
                    isActive
                      ? `${ws.path} — click to return to the chat, double-click for a new chat`
                      : `${ws.path} — click to browse, double-click for a new chat`
                  }
                >
                  <span className="ws-caret">{collapsed ? "▸" : "▾"}</span>
                  <span className="ws-section-name">
                    {labels[ws.path] ?? ws.name}
                  </span>
                  {ws.missing ? (
                    <span
                      className="ws-gone"
                      title="Directory no longer exists"
                    >
                      gone
                    </span>
                  ) : null}
                  {terminalWorkspaces[ws.path] ? (
                    <span
                      className="ws-terminal"
                      aria-label="terminal open"
                      title="A terminal is open here"
                    >
                      &gt;_
                    </span>
                  ) : null}
                  {busyWorkspaces[ws.path] ? (
                    <span
                      className="ws-live"
                      aria-label="agent working"
                      title="An agent is working here"
                    />
                  ) : null}
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
                    title="New thread (⌘N)"
                  >
                    +
                  </button>
                ) : (
                  <button
                    className="btn"
                    onClick={() => onOpenWorkspace(ws.path)}
                    title={`Open a chat in ${ws.path}`}
                  >
                    Chat
                  </button>
                )}
                {/* Closing is offered for the workspace you are in as well:
                    it ends the conversations open here and moves the window
                    on, and the threads stay on disk either way. */}
                <button
                  className="ws-pin"
                  onClick={() => onForget(ws.path)}
                  title="Close this workspace and the chats open in it"
                >
                  ✕
                </button>
              </div>
              {!collapsed ? (
                <div
                  className="thread-list"
                  // Double-clicking the empty room under a workspace starts a
                  // conversation in it, the same as double-clicking its name.
                  onDoubleClick={(event) => {
                    if ((event.target as HTMLElement).closest(".thread"))
                      return;
                    onNewIn(ws.path);
                  }}
                  title="Double-click for a new chat here"
                >
                  {list.length === 0 ? (
                    <div className="empty">no threads yet</div>
                  ) : null}
                  {list.map((t) => {
                    const current = t.thread_id === activeID && isActive;
                    const attention = threadAttention[t.thread_id];
                    return (
                      <div
                        id={`thread-search-${t.thread_id}`}
                        key={t.thread_id}
                        className={`thread ${current ? "active" : ""} ${
                          picked.has(`t:${t.thread_id}`) ? "picked" : ""
                        } ${dragID === t.thread_id ? "dragging" : ""} ${
                          selectedSearchID === t.thread_id
                            ? "search-selected"
                            : ""
                        }`}
                        draggable={!selecting && renaming !== t.thread_id}
                        onDragStart={() => setDragID(t.thread_id)}
                        onDragEnd={() => setDragID("")}
                        onDragOver={(e) => e.preventDefault()}
                        onDrop={() => dropBefore(t.thread_id)}
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
                        {/* A fixed leading gutter: the dots line up down the
                            edge of the list instead of drifting with the
                            length of each title. */}
                        <span
                          className={`thread-status ${threadStatus[t.thread_id] ?? "none"}`}
                          title={threadStatus[t.thread_id] ?? ""}
                        />
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
                            onBlur={(e) => {
                              const name = e.currentTarget.value.trim();
                              setRenaming("");
                              if (name && name !== t.title)
                                onRename(t.thread_id, name);
                            }}
                          />
                        ) : (
                          <button
                            className="thread-open"
                            onClick={() => {
                              if (selecting) {
                                if (!current) toggle(`t:${t.thread_id}`);
                                return;
                              }
                              onOpenThread(t.thread_id);
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
                              {attention && attention.kind !== "idle" ? (
                                <span
                                  className={`thread-attention ${attention.kind}`}
                                  title={attention.label}
                                >
                                  {attention.label}
                                  {attention.pending > 0
                                    ? ` (${attention.pending})`
                                    : ""}
                                </span>
                              ) : null}
                            </span>
                          </button>
                        )}
                        {selecting ? null : (
                          <button
                            className="thread-x"
                            title="Delete thread"
                            onClick={() => onDelete(t.thread_id)}
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
        {hidden > 0 || showAll ? (
          <button
            className="ws-more"
            onClick={() => setShowAll((on) => !on)}
            title="Sections are one per directory a thread has run in"
          >
            {showAll ? "Show fewer" : `Show all (${hidden} more)`}
          </button>
        ) : null}
      </div>
      {selecting ? (
        <BulkBar
          threadCount={pickedThreads.length}
          dirCount={pickedDirs.length}
          totalThreads={threads.length}
          selectable={selectableCount}
          onAddWorkspace={onAddWorkspace}
          onCreateFolder={startCreateFolder}
          onSelectAll={selectEverything}
          onCancel={leaveSelect}
          onDelete={() => {
            onBulkDelete(pickedThreads, pickedDirs);
            leaveSelect();
          }}
          onClearThreads={() => {
            onClearThreads();
            leaveSelect();
          }}
        />
      ) : null}
      {pendingParent ? (
        <NameDialog
          title="Create workspace folder"
          confirmLabel="Create folder"
          onConfirm={confirmCreateFolder}
          onCancel={() => setPendingParent("")}
        />
      ) : null}
    </aside>
  );
}

function BulkBar({
  threadCount,
  dirCount,
  totalThreads,
  selectable,
  onAddWorkspace,
  onCreateFolder,
  onSelectAll,
  onCancel,
  onDelete,
  onClearThreads,
}: {
  threadCount: number;
  dirCount: number;
  totalThreads: number;
  selectable: number;
  onAddWorkspace: () => void;
  onCreateFolder: () => void;
  onSelectAll: () => void;
  onCancel: () => void;
  onDelete: () => void;
  onClearThreads: () => void;
}) {
  const [confirm, setConfirm] = useState(false);
  // Separate confirmation: this one ignores the selection and the sections
  // entirely, so agreeing to it is a different decision.
  const [confirmAll, setConfirmAll] = useState(false);
  const total = threadCount + dirCount;
  const parts = [
    threadCount ? `${threadCount} thread${threadCount === 1 ? "" : "s"}` : "",
    dirCount ? `${dirCount} workspace${dirCount === 1 ? "" : "s"}` : "",
  ].filter(Boolean);

  if (confirmAll) {
    return (
      <div className="bulk-bar">
        <span className="bulk-count">
          Delete every stored chat except the one you are in? Workspaces stay in
          the list.
        </span>
        <button className="btn danger" onClick={onClearThreads}>
          Delete all
        </button>
        <button className="btn" onClick={() => setConfirmAll(false)}>
          Keep
        </button>
      </div>
    );
  }

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
            className="btn"
            onClick={onAddWorkspace}
            title="Add a folder and discover its Git repositories"
          >
            Add folder…
          </button>
          <button
            className="btn"
            onClick={onCreateFolder}
            title="Pick a parent directory, then name a new workspace folder"
          >
            Create folder…
          </button>
          <button
            className="btn danger"
            disabled={total === 0}
            onClick={() => setConfirm(true)}
          >
            Remove
          </button>
          <button
            className="btn"
            disabled={selectable === 0}
            onClick={onSelectAll}
            title="Select every workspace and chat except the ones you are in"
          >
            Select all{selectable ? ` (${selectable})` : ""}
          </button>
          <button
            className="btn danger"
            onClick={() => setConfirmAll(true)}
            title="Delete every stored chat, including ones with no section here. Workspaces stay in the list."
          >
            All chats{totalThreads ? ` (${totalThreads})` : ""}
          </button>
          <button className="btn" onClick={onCancel}>
            Cancel
          </button>
        </>
      )}
    </div>
  );
}
