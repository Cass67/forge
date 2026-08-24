// Open-session bookkeeping for the sidebar.
//
// Every open session has its own live chat runtime, so any number of them can
// be running at once. The backend is the authority on which are live and what
// they are doing; what is kept here is this window's memory of sessions it has
// seen, so a thread that finished while it was on screen still shows how it
// ended after the runtime is gone.

export type SessionStatus = "idle" | "working" | "waiting" | "failed" | "done";

export type SessionTabState = {
  open: string[];
  status: Record<string, SessionStatus>;
};

export const SESSION_TABS_KEY = "forge.sessionTabs";

// A conversation that has not been written to the thread store yet has no id.
// It still needs a tab, so it gets this one until the runtime hands one over.
export const NEW_SESSION_ID = "";

export const emptyTabs: SessionTabState = { open: [], status: {} };

// Tabs are per workspace: a thread id from another directory has nothing to
// restore in this window.
function storageKey(workDir: string): string {
  return `${SESSION_TABS_KEY}:${workDir}`;
}

export function loadTabs(workDir: string): SessionTabState {
  if (!workDir) return emptyTabs;
  try {
    const raw = JSON.parse(localStorage.getItem(storageKey(workDir)) ?? "null");
    if (!raw || !Array.isArray(raw.open)) return emptyTabs;
    return {
      open: raw.open.filter(
        (id: unknown): id is string => typeof id === "string",
      ),
      status: typeof raw.status === "object" && raw.status ? raw.status : {},
    };
  } catch {
    return emptyTabs;
  }
}

export function saveTabs(workDir: string, state: SessionTabState): void {
  if (!workDir) return;
  localStorage.setItem(storageKey(workDir), JSON.stringify(state));
}

export function openTab(state: SessionTabState, id: string): SessionTabState {
  if (!id || state.open.includes(id)) return state;
  return { ...state, open: [...state.open, id] };
}

// closeTab forgets the tab, not the thread: the sidebar still lists it and
// deleting is a separate, confirmed action.
export function closeTab(state: SessionTabState, id: string): SessionTabState {
  if (!state.open.includes(id)) return state;
  const status = { ...state.status };
  delete status[id];
  return { open: state.open.filter((open) => open !== id), status };
}

export function setStatus(
  state: SessionTabState,
  id: string,
  status: SessionStatus,
): SessionTabState {
  if (!id || state.status[id] === status) return state;
  return { ...state, status: { ...state.status, [id]: status } };
}

// pruneTabs drops ids whose threads no longer exist, which is what a deleted
// or externally removed thread looks like from here.
export function pruneTabs(
  state: SessionTabState,
  known: string[],
): SessionTabState {
  const live = new Set(known);
  const open = state.open.filter((id) => live.has(id));
  if (open.length === state.open.length) return state;
  const status: Record<string, SessionStatus> = {};
  for (const id of open) if (state.status[id]) status[id] = state.status[id];
  return { open, status };
}

// nextAfterClose picks which tab to focus once id goes away.
export function nextAfterClose(state: SessionTabState, id: string): string {
  const index = state.open.indexOf(id);
  if (index < 0) return "";
  const remaining = state.open.filter((open) => open !== id);
  return remaining[Math.min(index, remaining.length - 1)] ?? "";
}
