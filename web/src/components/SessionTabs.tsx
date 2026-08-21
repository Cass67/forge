import type { ThreadSummary } from "../bridge";
import {
  NEW_SESSION_ID,
  type SessionStatus,
  type SessionTabState,
} from "../sessionTabs";

type Props = {
  tabs: SessionTabState;
  threads: ThreadSummary[];
  activeID: string;
  onSelect: (id: string) => void;
  onClose: (id: string) => void;
  onNew: () => void;
};

const statusLabel: Record<SessionStatus, string> = {
  idle: "idle",
  working: "working",
  waiting: "waiting for you",
  failed: "failed",
  done: "finished",
};

// SessionTabs is the strip of open conversations above the transcript. Only
// the focused tab is live — see sessionTabs.ts — so the others carry the last
// status they were seen in rather than a running one.
export function SessionTabs({
  tabs,
  threads,
  activeID,
  onSelect,
  onClose,
  onNew,
}: Props) {
  const byID = new Map(threads.map((thread) => [thread.thread_id, thread]));
  // A brand-new conversation has no stored thread id yet. It still gets a tab,
  // appended last and always the active one, so starting a session looks like
  // something happened; it joins tabs.open once the runtime names it.
  const ids = tabs.open.includes(activeID)
    ? tabs.open
    : [...tabs.open, activeID];
  if (ids.length === 0) return null;

  return (
    <div className="session-tabs" role="tablist">
      {ids.map((id) => {
        const thread = byID.get(id);
        const status = tabs.status[id] ?? "idle";
        const unsaved = id === NEW_SESSION_ID;
        const title = thread?.title || "new session";
        return (
          <div
            className={`session-tab ${id === activeID ? "active" : ""}`}
            key={id}
          >
            <button
              role="tab"
              aria-selected={id === activeID}
              onClick={() => onSelect(id)}
              title={`${title} — ${statusLabel[status]}`}
            >
              <span className={`session-dot ${status}`} aria-hidden="true" />
              <span className="session-title">{title}</span>
            </button>
            {unsaved ? null : (
              <button
                className="workspace-tab-close"
                onClick={() => onClose(id)}
                aria-label={`Close ${title}`}
              >
                ×
              </button>
            )}
          </div>
        );
      })}
      <button className="session-add" onClick={onNew} title="New session">
        +
      </button>
    </div>
  );
}
