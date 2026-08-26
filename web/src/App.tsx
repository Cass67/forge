import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  forge,
  type InitPayload,
  type SessionInfo,
  type ThreadSummary,
  type WireAction,
  type WireEvent,
  type Workspace,
  type Attachment,
} from "./bridge";
import { applyEvent, userEntry, type Entry } from "./entries";
import { itemsToEntries } from "./replay";
import { Sidebar } from "./components/Sidebar";
import {
  closeTab as closeSessionTab,
  emptyTabs,
  loadTabs,
  nextAfterClose,
  openTab,
  pruneTabs,
  saveTabs,
  type SessionStatus,
  type SessionTabState,
  setStatus,
} from "./sessionTabs";
import { Transcript } from "./components/Transcript";
import { ApprovalModal } from "./components/ApprovalModal";
import { Composer } from "./components/Composer";
import { StatsBar } from "./components/StatsBar";
import { ActivityPanel } from "./components/ActivityPanel";
import { ModelPicker } from "./components/ModelPicker";
import { SettingsPanel, type Prefs } from "./components/SettingsPanel";
import { HelpOverlay } from "./components/HelpOverlay";
import { WorkspaceMenu } from "./components/WorkspaceMenu";
import { WorkspaceShell } from "./components/WorkspaceShell";
import { deriveAttention, type Attention } from "./attention";
import {
  applyTheme,
  applyVividness,
  isTheme,
  loadTheme,
  nextTheme,
  type Theme,
} from "./theme";
import { clampVividness, loadVividness } from "./vividness";
import {
  clampSidebarWidth,
  DEFAULT_SIDEBAR_WIDTH,
  parseSidebarWidth,
  SIDEBAR_STORAGE_KEY,
} from "./sidebarLayout";
import {
  applyScale,
  clampScale,
  formatScale,
  loadScale,
  step,
  DEFAULT_SCALE,
} from "./scale";
import {
  applyStatsEvent,
  emptyStats,
  setStatsModel,
  type SessionStats,
} from "./sessionStats";
import { initialDockVisibility, initialYoloState } from "./dockVisibility";

// flag sets one key of a boolean map, returning the map untouched when the
// value is already what it should be. React re-renders on identity, so this is
// the difference between a state update per streamed token and one per change.
function flag(
  current: Record<string, boolean>,
  key: string,
  value: boolean,
): Record<string, boolean> {
  return current[key] === value ? current : { ...current, [key]: value };
}

const defaultPrefs: Prefs = {
  showTools: true,
  showReasoning: true,
  expandReasoning: true,
  expandTools: false,
  showActivity: true,
  showSidebar: true,
  showDocks: true,
  yolo: false,
  scopeThreads: true,
  expandSubfolders: false,
  autoDiscoverSubfolders: false,
  dockLayoutPersistence: "default",
};

function loadPrefs(): Prefs {
  try {
    return {
      ...defaultPrefs,
      ...JSON.parse(localStorage.getItem("forge.prefs") ?? "{}"),
    };
  } catch {
    return defaultPrefs;
  }
}

type Overlay = "none" | "models" | "settings" | "help" | "workspaces";

export default function App() {
  const [init, setInit] = useState<InitPayload | null>(null);
  const [entries, setEntries] = useState<Entry[]>([]);
  const [threads, setThreads] = useState<ThreadSummary[]>([]);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [activeID, setActiveID] = useState("");
  const [tabs, setTabs] = useState<SessionTabState>(emptyTabs);
  const [busy, setBusy] = useState(false);
  // Bumped on every new-session request so the composer takes focus.
  const [composerFocus, setComposerFocus] = useState(0);
  // How far the theme is lifted out of its designed palette. Applied on top of
  // whichever theme is set, so it survives switching between them.
  const [vividness, setVividnessState] = useState(loadVividness);
  const [approvalQueues, setApprovalQueues] = useState<
    Record<string, WireAction[]>
  >({});
  const [submittingApproval, setSubmittingApproval] = useState(false);
  const [statsBySession, setStatsBySession] = useState<SessionStats>({});
  const [noticeSeen, setNoticeSeen] = useState(false);
  const [effort, setEffort] = useState("");
  const [yolo, setYoloState] = useState(() =>
    initialYoloState(loadPrefs().yolo),
  );
  const [overlay, setOverlay] = useState<Overlay>("none");
  const [theme, setThemeState] = useState<Theme>(loadTheme);
  const [scale, setScaleState] = useState<number>(loadScale);
  const [prefs, setPrefsState] = useState<Prefs>(loadPrefs);
  const [flash, setFlash] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const [pending, setPending] = useState<Attachment[]>([]);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  // Which workspaces have an agent turn in flight, keyed by directory. A
  // runtime keeps working while its workspace is in the background, so this
  // drives the liveness dots instead of the focused pane's busy flag.
  const [busyDirs, setBusyDirs] = useState<Record<string, boolean>>({});
  const [openTerminals, setOpenTerminals] = useState<
    Record<string, Record<string, boolean>>
  >({});
  const openTerminalsRef = useRef(openTerminals);
  openTerminalsRef.current = openTerminals;
  // Sub-agent roles that have produced output in the turn on screen. Cleared
  // when the turn ends, which is the only signal the event stream gives that
  // delegation is over.
  const [agentsBySession, setAgentsBySession] = useState<
    Record<string, string[]>
  >({});
  // Which live sessions have a turn in flight. A background session keeps
  // working, so this is what the sidebar dots read.
  const [busySessions, setBusySessions] = useState<Record<string, boolean>>({});
  // Every live session, from the backend. Several can share a workspace.
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const sessionsRef = useRef(sessions);
  sessionsRef.current = sessions;
  // The workspace the panels are browsing, empty when they follow the chat.
  const [browseDir, setBrowseDir] = useState("");
  // Transcripts stashed per session, so going to another conversation and back
  // restores what it streamed instead of replaying it from storage.
  const sessionCache = useRef(
    new Map<string, { entries: Entry[]; activeID: string }>(),
  );
  const prevSessionRef = useRef("");
  const [completedAt, setCompletedAt] = useState<Record<string, number>>({});
  const [visitedAt, setVisitedAt] = useState<Record<string, number>>({});
  const switchingWorkspace = useRef(false);
  const scaleRef = useRef(scale);
  scaleRef.current = scale;
  const prefsRef = useRef(prefs);
  prefsRef.current = prefs;
  const dragDepth = useRef(0);
  const [dragging, setDragging] = useState(false);
  const [workspaceMode, setWorkspaceMode] = useState(() =>
    initialDockVisibility(loadPrefs().showDocks),
  );
  const [workspaceDirty, setWorkspaceDirty] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(() =>
    parseSidebarWidth(
      localStorage.getItem(SIDEBAR_STORAGE_KEY),
      window.innerWidth,
    ),
  );

  useEffect(() => applyTheme(theme), [theme]);
  useEffect(() => applyScale(scale), [scale]);
  useEffect(
    () => localStorage.setItem("forge.prefs", JSON.stringify(prefs)),
    [prefs],
  );
  useEffect(
    () => localStorage.setItem(SIDEBAR_STORAGE_KEY, String(sidebarWidth)),
    [sidebarWidth],
  );

  const startSidebarDrag = (event: React.PointerEvent) => {
    event.preventDefault();
    const move = (pointer: PointerEvent) =>
      setSidebarWidth(clampSidebarWidth(pointer.clientX, window.innerWidth));
    const stop = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", stop);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", stop);
  };

  const resizeSidebarWithKeys = (event: React.KeyboardEvent) => {
    const delta =
      event.key === "ArrowLeft" ? -20 : event.key === "ArrowRight" ? 20 : 0;
    if (!delta) return;
    event.preventDefault();
    setSidebarWidth((width) =>
      clampSidebarWidth(width + delta, window.innerWidth),
    );
  };

  const notify = useCallback((msg: string) => {
    setFlash(msg);
    setTimeout(() => setFlash(""), 2600);
  }, []);

  const attachPaths = useCallback(
    async (paths: string[]) => {
      for (const path of paths) {
        if (typeof path !== "string" || !path.trim()) continue;
        try {
          const att = await forge.attachPath(path);
          setPending((p) => (p.some((x) => x.id === att.id) ? p : [...p, att]));
        } catch (e) {
          notify(`${path.split("/").pop()}: ${String(e)}`);
        }
      }
    },
    [notify],
  );

  const workDir = init?.work_dir ?? "";
  // Unfinished input is per chat session, not per workspace: switching
  // workspaces mid-typing must not carry someone else's draft over.
  const chatKey = `${workDir}::${activeID}`;

  // Tabs live per workspace, and are reloaded when the window switches to one.
  useEffect(() => setTabs(loadTabs(workDir)), [workDir]);
  useEffect(() => saveTabs(workDir, tabs), [workDir, tabs]);

  // The active conversation is always on screen, so it always has a tab.
  useEffect(() => {
    if (activeID) setTabs((current) => openTab(current, activeID));
  }, [activeID]);

  // A thread deleted from the sidebar leaves a tab pointing at nothing.
  useEffect(() => {
    setTabs((current) =>
      pruneTabs(
        current,
        threads.map((thread) => thread.thread_id),
      ),
    );
  }, [threads]);

  const markActive = useCallback(
    (status: SessionStatus) =>
      setTabs((current) =>
        activeID ? setStatus(current, activeID, status) : current,
      ),
    [activeID],
  );

  // Held in a ref so the event subscription does not tear down and re-attach
  // every time the focused session changes.

  // The session on screen. Held in a ref as well so the event subscription can
  // route by it without tearing down and re-attaching on every switch.
  const sessionID = init?.session ?? "";
  const sessionRef = useRef(sessionID);
  sessionRef.current = sessionID;
  const stats = statsBySession[sessionID] ?? emptyStats(init?.model);
  const subAgents = agentsBySession[sessionID] ?? [];
  const approvalQueue = approvalQueues[sessionID] ?? [];
  const approval = approvalQueue[0] ?? null;

  // A background session's output is folded into its cached transcript, so
  // coming back to it shows everything it streamed while it was away.
  const cacheBackground = useCallback((id: string, ev: WireEvent) => {
    const cached = sessionCache.current.get(id);
    sessionCache.current.set(id, {
      entries: applyEvent(cached?.entries ?? [], ev),
      activeID: cached?.activeID ?? "",
    });
  }, []);

  const markRef = useRef(markActive);
  markRef.current = markActive;
  const entriesRef = useRef(entries);
  entriesRef.current = entries;
  const activeIDRef = useRef(activeID);
  activeIDRef.current = activeID;
  // Whether anything in the current turn failed, which decides between the
  // "finished" and "failed" dot when the turn ends.
  const turnFailed = useRef(false);

  const refreshThreads = useCallback(() => {
    void forge.threads().then(setThreads);
    const list = prefsRef.current.autoDiscoverSubfolders
      ? forge.refreshWorkspaceTrees()
      : forge.workspaces();
    void list.then(setWorkspaces);
  }, []);

  useEffect(() => {
    if (!prefs.autoDiscoverSubfolders) return;
    const scan = () => {
      void forge.refreshWorkspaceTrees().then(setWorkspaces);
    };
    scan();
    window.addEventListener("focus", scan);
    const timer = window.setInterval(scan, 10_000);
    return () => {
      window.removeEventListener("focus", scan);
      window.clearInterval(timer);
    };
  }, [prefs.autoDiscoverSubfolders]);

  const loadInit = useCallback(() => {
    void forge.init().then((payload) => {
      if (!payload.ready) return;
      const session = payload.session ?? "";
      switchingWorkspace.current = false;
      setInit(payload);
      setNoticeSeen(false);
      setStatsBySession((current) =>
        setStatsModel(current, session, payload.model),
      );
      if (payload.effort) setEffort(payload.effort);
      const firstInit = !prevSessionRef.current;
      setYoloState(firstInit ? prefsRef.current.yolo : payload.yolo);
      if (firstInit && payload.yolo !== prefsRef.current.yolo) {
        void forge
          .setYolo(prefsRef.current.yolo)
          .catch((e: unknown) => notify(String(e)));
      }
      // Stash the outgoing session's transcript so coming back to it shows
      // what it streamed rather than a replay from storage.
      if (prevSessionRef.current && prevSessionRef.current !== session) {
        sessionCache.current.set(prevSessionRef.current, {
          entries: entriesRef.current,
          activeID: activeIDRef.current,
        });
      }
      const switched = prevSessionRef.current !== session;
      prevSessionRef.current = session;
      if (switched) {
        // Typing goes to the conversation that just came on screen, launch
        // included: the composer used to sit unfocused until it was clicked.
        setComposerFocus((n) => n + 1);
        // busy and any delegation belong to the session that was on screen;
        // its own dot keeps tracking it, but nothing here must stay locked.
        setBusy(false);
        // The backend hands the panels back to the chat on a deliberate
        // switch, so the window must not keep claiming it is browsing.
        setBrowseDir("");
      }
      const saved = sessionCache.current.get(session);
      const savedMatches =
        saved && (!payload.thread_id || saved.activeID === payload.thread_id);
      if (savedMatches) {
        setActiveID(saved.activeID);
        setEntries(saved.entries);
      } else if (payload.thread_id) {
        setActiveID(payload.thread_id);
        void forge
          .history(payload.thread_id)
          .then((items) => setEntries(itemsToEntries(items)));
      } else {
        // A session with nothing in it yet starts on an empty transcript. The
        // backend already made it, so nothing is asked for here.
        setActiveID("");
        setEntries([]);
      }
      void forge.sessions().then(setSessions);
      refreshThreads();
    });
  }, [refreshThreads]);

  useEffect(() => {
    loadInit();
    const offReady = forge.onReady(loadInit);
    const offEvent = forge.onEvent((ev) => {
      const from = ev.session ?? "";
      const dir = ev.workspace ?? "";
      const finished =
        ev.kind === "done" || ev.kind === "agent_done" || ev.kind === "abort";
      // Every token of every live session comes through here. Replacing these
      // maps unconditionally re-rendered the whole sidebar per token, so they
      // are only replaced when the answer actually changes.
      if (from) setBusySessions((m) => flag(m, from, !finished));
      if (dir) setBusyDirs((m) => flag(m, dir, !finished));
      if (from && ev.sub_agent) {
        const role = ev.sub_agent;
        setAgentsBySession((current) => {
          const roles = current[from] ?? [];
          return roles.includes(role)
            ? current
            : { ...current, [from]: [...roles, role] };
        });
      }
      if (from && finished)
        setAgentsBySession((current) => ({ ...current, [from]: [] }));
      // Usage belongs to its producing session even while that session runs
      // behind another workspace. The status bar selects only the map entry
      // for the conversation currently on screen.
      if (ev.kind === "stats") {
        const statsSession = from || sessionRef.current;
        setStatsBySession((current) =>
          applyStatsEvent(current, statsSession, ev),
        );
      }
      // Background sessions keep streaming into their own caches: their
      // output must never land in the transcript on screen.
      if (from && from !== sessionRef.current) {
        cacheBackground(from, ev);
        return;
      }
      if (switchingWorkspace.current) return;
      setEntries((prev) => applyEvent(prev, ev));
      if (ev.is_error || ev.error) turnFailed.current = true;
      if (finished) {
        setBusy(false);
        // The backend hands the panels back to the chat on a deliberate
        // switch, so the window must not keep claiming it is browsing.
        setBrowseDir("");
        markRef.current(turnFailed.current ? "failed" : "done");
        refreshThreads();
      }
    });
    const offSessions = forge.onSessions(setSessions);
    // The webview never gives the DOM a dragged file; Wails delivers the
    // paths here instead.
    const offFiles = forge.onFilesDropped((paths) => {
      setDragging(false);
      dragDepth.current = 0;
      void attachPaths(paths);
    });
    const offApproval = forge.onApproval((action) => {
      const from = action.session ?? "";
      if (!from) return;
      setApprovalQueues((current) => ({
        ...current,
        [from]: [...(current[from] ?? []), action],
      }));
      setBusySessions((m) => flag(m, from, true));
      if (action.workspace)
        setBusyDirs((m) => flag(m, action.workspace as string, true));
      if (from !== sessionRef.current) return;
      if (switchingWorkspace.current) return;
      markRef.current("waiting");
    });
    const offDone = forge.onTurnDone((done) => {
      const from = done.session ?? "";
      const tag = done.workspace ?? "";
      if (from) setBusySessions((m) => flag(m, from, false));
      if (tag) setBusyDirs((m) => flag(m, tag, false));
      const threadID = sessionsRef.current.find(
        (session) => session.id === from,
      )?.thread_id;
      if (threadID) {
        const now = Date.now();
        setCompletedAt((current) => ({ ...current, [threadID]: now }));
        if (from === sessionRef.current)
          setVisitedAt((current) => ({ ...current, [threadID]: now }));
      }
      // A background session finishing its turn only clears its own dot.
      if (!from || from === sessionRef.current) {
        setBusy(false);
        markRef.current(turnFailed.current ? "failed" : "done");
        refreshThreads();
      }
    });
    return () => {
      offReady();
      offEvent();
      offSessions();
      offFiles();
      offApproval();
      offDone();
    };
  }, [attachPaths, loadInit, refreshThreads]);

  const sendInput = useCallback(
    (text: string) => {
      const images = pending;
      setEntries((prev) => [
        ...prev,
        userEntry(
          text,
          images.map((a) => a.path),
        ),
      ]);
      setHistory((h) => [...h, text]);
      setBusy(true);
      const dir = init?.work_dir ?? "";
      if (dir) setBusyDirs((m) => flag(m, dir, true));
      // The draft belongs to this chat session; sending it clears it so the
      // composer is empty for the next message.
      setDrafts((current) =>
        current[chatKey] ? { ...current, [chatKey]: "" } : current,
      );
      turnFailed.current = false;
      markRef.current("working");
      setPending([]);
      const p =
        images.length > 0
          ? forge.sendWithImages(text, images)
          : forge.send(text);
      void p.catch((e: unknown) => {
        setBusy(false);
        notify(String(e));
      });
    },
    [notify, pending],
  );

  const approve = (ok: boolean) => {
    if (!approval || submittingApproval) return;
    setSubmittingApproval(true);
    void forge
      .approve(approval.id, ok)
      .then(() => {
        setApprovalQueues((current) => ({
          ...current,
          [sessionID]: (current[sessionID] ?? []).filter(
            (candidate) => candidate.id !== approval.id,
          ),
        }));
        markRef.current("working");
      })
      .catch((e: unknown) => notify(String(e)))
      .finally(() => setSubmittingApproval(false));
  };

  const cancel = useCallback(() => void forge.cancel(), []);
  const newThread = useCallback(() => {
    // Optimistic: the empty-session tab exists before anything is sent.
    setEntries([]);
    setActiveID("");
    setComposerFocus((n) => n + 1);
    void forge
      .newSession()
      .then(refreshThreads)
      .catch((e: unknown) => notify(String(e)));
  }, [notify, refreshThreads]);

  // Opening a thread activates its live session if it has one, and otherwise
  // starts a session to resume it. Either way the session that was on screen
  // keeps running: navigating never interrupts a turn.
  const restoreThread = useCallback(
    (id: string) => {
      void forge
        .restore(id)
        .then((r) => {
          // Paint straight away; the session's own events take over once it
          // attaches and the window re-inits against it.
          setEntries(itemsToEntries(r.items));
          setActiveID(r.thread_id);
          setVisitedAt((current) => ({
            ...current,
            [r.thread_id]: Date.now(),
          }));
          refreshThreads();
          void forge.sessions().then(setSessions);
        })
        .catch((e: unknown) => notify(String(e)));
    },
    [notify, refreshThreads],
  );

  const openExactThread = useCallback(
    (id: string) => {
      const thread = threads.find((candidate) => candidate.thread_id === id);
      if (!thread || thread.cwd === workDir) {
        restoreThread(id);
        return;
      }
      void forge
        .openThread(id)
        .then(() => {
          setVisitedAt((current) => ({ ...current, [id]: Date.now() }));
          refreshThreads();
        })
        .catch((e: unknown) => notify(String(e)));
    },
    [notify, refreshThreads, restoreThread, threads, workDir],
  );

  // Sidebar dots. A thread with a live session shows what that session is
  // really doing; the rest fall back to the last state this window saw.
  const threadStatus = useMemo(() => {
    const merged: Record<string, SessionStatus> = { ...tabs.status };
    for (const session of sessions) {
      if (!session.thread_id) continue;
      merged[session.thread_id] = busySessions[session.id]
        ? "working"
        : (merged[session.thread_id] ?? "idle");
    }
    return merged;
  }, [busySessions, sessions, tabs.status]);

  const threadAttention = useMemo(() => {
    const result: Record<string, Attention> = {};
    for (const thread of threads) {
      const session = sessions.find(
        (candidate) => candidate.thread_id === thread.thread_id,
      );
      result[thread.thread_id] = deriveAttention({
        pendingApprovals: session
          ? (approvalQueues[session.id]?.length ?? 0)
          : 0,
        status: threadStatus[thread.thread_id] ?? "idle",
        completedAt: completedAt[thread.thread_id] ?? 0,
        visitedAt: visitedAt[thread.thread_id] ?? 0,
      });
    }
    return result;
  }, [approvalQueues, completedAt, sessions, threadStatus, threads, visitedAt]);

  // A thread is open if it has a live session, whatever this window remembers.
  const openThreadIDs = useMemo(() => {
    const ids = new Set(tabs.open);
    for (const session of sessions) {
      if (session.thread_id) ids.add(session.thread_id);
    }
    return [...ids];
  }, [sessions, tabs.open]);

  const terminalDirs = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(openTerminals).map(([dir]) => [dir, true]),
      ),
    [openTerminals],
  );

  const setTerminalPresence = useCallback(
    (dir: string, id: string, open: boolean) =>
      setOpenTerminals((current) => {
        if (!open && !current[dir]?.[id]) return current;
        const terminals = { ...(current[dir] ?? {}) };
        if (open) terminals[id] = true;
        else delete terminals[id];
        if (Object.keys(terminals).length === 0) {
          const next = { ...current };
          delete next[dir];
          return next;
        }
        return { ...current, [dir]: terminals };
      }),
    [],
  );

  const closeTerminalPanel = useCallback((dir: string, panelID: string) => {
    const ids = Object.keys(openTerminalsRef.current[dir] ?? {}).filter((id) =>
      id.startsWith(`${panelID}:`),
    );
    for (const id of ids) void forge.closeTerminal(id);
    setOpenTerminals((current) => {
      const terminals = { ...(current[dir] ?? {}) };
      for (const id of ids) delete terminals[id];
      const next = { ...current };
      if (Object.keys(terminals).length > 0) next[dir] = terminals;
      else delete next[dir];
      return next;
    });
  }, []);

  useEffect(
    () =>
      forge.onTerminal((event) => {
        if (!event.closed || !event.workspace) return;
        setTerminalPresence(event.workspace, event.id, false);
      }),
    [setTerminalPresence],
  );

  // Looking at a workspace's files without starting a chat in it. The panels
  // follow; the conversation on screen stays where it is.
  const browseWorkspace = useCallback(
    (dir: string) => {
      const target = dir === (init?.work_dir ?? "") ? "" : dir;
      void forge
        .setExplorerRoot(target)
        .then(() => setBrowseDir(target))
        .catch((e: unknown) => notify(String(e)));
    },
    [init?.work_dir, notify],
  );

  // Closing a workspace ends every conversation open in it. Confirmed when one
  // of them is mid-turn, since that work is lost.
  const closeWorkspace = useCallback(
    (dir: string) => {
      const live = sessions.filter((session) => session.workspace === dir);
      const working = live.some((session) => busySessions[session.id]);
      if (
        working &&
        !window.confirm(
          `An agent is still working in ${dir.split("/").pop()}. Close it anyway?`,
        )
      )
        return;
      void forge
        .closeWorkspace(dir)
        .then((next) => {
          setWorkspaces(next);
          refreshThreads();
        })
        .catch((e: unknown) => notify(String(e)));
    },
    [busySessions, notify, refreshThreads, sessions],
  );

  // Which live session, if any, is running a given thread.
  const sessionForThread = useCallback(
    (threadID: string) =>
      sessions.find((session) => session.thread_id === threadID)?.id ?? "",
    [sessions],
  );

  // Closing a chat ends its session and forgets the tab; the thread itself
  // stays on disk. Shared by the sidebar's ✕ and the tab strip.
  const closeChat = useCallback(
    (id: string) => {
      const next = nextAfterClose(tabs, id);
      const live = sessionForThread(id);
      setTabs((current) => closeSessionTab(current, id));
      if (live) {
        void forge.closeSession(live).catch((e: unknown) => notify(String(e)));
        // The backend hands the window to another session in this workspace
        // and re-inits; nothing more to do when the closed one was on screen.
        return;
      }
      if (id === activeID) {
        if (next) restoreThread(next);
        else newThread();
      }
    },
    [activeID, newThread, notify, restoreThread, sessionForThread, tabs],
  );

  const deleteThread = useCallback(
    (id: string) => {
      void forge
        .deleteThread(id)
        .then((next) => {
          setThreads(next);
          notify("thread deleted");
        })
        .catch((e: unknown) => notify(String(e)));
    },
    [notify],
  );

  // "Remove all" is one call, not one per thread: the sidebar can only tick
  // threads whose workspace still has a section, so anything started in a
  // worktree or a scratch dir was unreachable from the list.
  const clearThreads = useCallback(() => {
    void forge
      .clearThreads()
      .then((res) => {
        setThreads(res.threads);
        notify(
          res.failed
            ? `removed ${res.removed}, ${res.failed} could not be removed`
            : `removed ${res.removed} thread${res.removed === 1 ? "" : "s"}`,
        );
      })
      .catch((e: unknown) => notify(String(e)));
  }, [notify]);

  // Bulk removal runs one call at a time: the thread store and the workspace
  // registry both rewrite a whole file per call, so parallel writes would race.
  const bulkDelete = useCallback(
    (threadIDs: string[], dirs: string[]) => {
      void (async () => {
        // A workspace's chats go with it. The list has a section for every
        // directory a stored thread ran in, so a folder whose chats survive
        // is derived straight back and the button looks broken.
        const doomed = new Set(threadIDs);
        for (const thread of threads) {
          if (thread.cwd && dirs.includes(thread.cwd))
            doomed.add(thread.thread_id);
        }
        let failedThreads = 0;
        for (const id of doomed) {
          try {
            setThreads(await forge.deleteThread(id));
          } catch {
            failedThreads++;
          }
        }
        let failedDirs = 0;
        for (const dir of dirs) {
          try {
            setWorkspaces(await forge.forgetWorkspace(dir));
          } catch (e: unknown) {
            failedDirs++;
            notify(String(e));
          }
        }
        const chats = doomed.size - failedThreads;
        const places = dirs.length - failedDirs;
        const said = [
          chats ? `${chats} chat${chats === 1 ? "" : "s"}` : "",
          places ? `${places} workspace${places === 1 ? "" : "s"}` : "",
        ].filter(Boolean);
        notify(
          said.length === 0
            ? "nothing was removed"
            : `removed ${said.join(" and ")}`,
        );
        refreshThreads();
      })();
    },
    [notify, refreshThreads, threads],
  );

  const switchModel = useCallback(
    (model: string) => {
      setOverlay("none");
      void forge
        .switchModel(model)
        .then((applied) => {
          setStatsBySession((current) =>
            setStatsModel(current, sessionID, applied),
          );
          setNoticeSeen(true);
          notify(`model → ${applied}`);
          return forge.efforts(applied);
        })
        .then((list) => setInit((i) => (i ? { ...i, efforts: list } : i)))
        .catch((e: unknown) => notify(String(e)));
    },
    [notify, sessionID],
  );

  const setEffortAction = useCallback(
    (e: string) => {
      setEffort(e);
      void forge.setEffort(e).catch((err: unknown) => notify(String(err)));
    },
    [notify],
  );

  const toggleYolo = useCallback(
    (on: boolean) => {
      setYoloState(on);
      void forge
        .setYolo(on)
        .then((now) => {
          setYoloState(now);
          notify(
            now
              ? "yolo on — tools run without asking"
              : "yolo off — tools ask first",
          );
        })
        .catch((e: unknown) => notify(String(e)));
    },
    [notify],
  );

  const setTheme = useCallback(
    (t: Theme) => {
      setThemeState(t);
      notify(`theme → ${t}`);
    },
    [notify],
  );

  const setScale = useCallback(
    (next: number) => {
      const value = clampScale(next);
      setScaleState(value);
      notify(`text size → ${formatScale(value)}`);
    },
    [notify],
  );

  // Switching activates another directory's runtime in this same window and
  // leaves the previous one running; the frontend re-initialises when the
  // backend signals the newly active runtime.
  const openWorkspace = useCallback(
    (dir: string) => {
      if (
        workspaceDirty &&
        !window.confirm(
          "Discard unsaved workspace changes and switch workspaces?",
        )
      )
        return;
      switchingWorkspace.current = true;
      notify(`opening ${dir.split("/").pop()}…`);
      void forge.switchWorkspace(dir).catch((e: unknown) => notify(String(e)));
    },
    [notify, workspaceDirty],
  );

  // Double-clicking a workspace starts a fresh session in it. Sessions are
  // independent, so the one on screen keeps running whichever workspace the
  // new one lands in.
  const newSessionIn = useCallback(
    (dir: string) => {
      setEntries([]);
      setActiveID("");
      void forge
        .newSession(dir)
        .then(refreshThreads)
        .catch((e: unknown) => notify(String(e)));
    },
    [notify, refreshThreads],
  );

  const addWorkspace = useCallback(() => {
    if (
      workspaceDirty &&
      !window.confirm(
        "Discard unsaved workspace changes and switch workspaces?",
      )
    )
      return;
    void forge
      .chooseWorkspace()
      .then(async (dir) => {
        if (!dir) return;
        setEntries([]);
        setActiveID("");
        if (!prefsRef.current.expandSubfolders) return;
        // A container folder registers everything under it and stays where it
        // is: chooseWorkspace has already switched to the folder itself, and
        // the repositories under it are a click away rather than all started.
        try {
          setWorkspaces(await forge.addWorkspaceTree(dir));
          notify("subfolders added as workspaces");
        } catch (e: unknown) {
          notify(String(e));
        }
      })
      .catch((e: unknown) => notify(String(e)));
  }, [notify, workspaceDirty]);

  const lastAgentText = useMemo(() => {
    for (let i = entries.length - 1; i >= 0; i--) {
      const e = entries[i];
      if (e.t === "text" && e.role === "agent") return e.text;
    }
    return "";
  }, [entries]);

  const setVividness = useCallback((level: number) => {
    const next = clampVividness(level);
    setVividnessState(next);
    applyVividness(next);
  }, []);

  const runCommand = useCallback(
    (raw: string) => {
      const [cmd, ...rest] = raw.trim().split(" ");
      const arg = rest.join(" ").trim();
      switch (cmd) {
        case "/new":
          return newThread();
        case "/clear":
          setEntries([]);
          void forge.clear();
          return notify("transcript cleared");
        case "/model":
        case "/models":
          if (arg) return switchModel(arg);
          return setOverlay("models");
        case "/effort":
          if (arg) return setEffortAction(arg);
          return setOverlay("settings");
        case "/theme":
          if (arg) {
            if (!isTheme(arg)) return notify(`unknown theme "${arg}"`);
            return setTheme(arg);
          }
          return setTheme(nextTheme(theme));
        case "/workspace":
        case "/workspaces":
          return setOverlay("workspaces");
        case "/threads":
        case "/sessions":
          return setPrefsState((p) => ({ ...p, showSidebar: !p.showSidebar }));
        case "/stats":
          return setPrefsState((p) => ({
            ...p,
            showActivity: !p.showActivity,
          }));
        case "/tools":
          return setPrefsState((p) => ({ ...p, showTools: !p.showTools }));
        case "/skills":
        case "/settings":
        case "/provider":
        case "/providers":
          return setOverlay("settings");
        case "/help":
          return setOverlay("help");
        case "/yolo":
          return toggleYolo(
            arg === "" ? !yolo : arg === "on" || arg === "true",
          );
        case "/cancel":
          return cancel();
        case "/copy":
          if (!lastAgentText) return notify("nothing to copy");
          void navigator.clipboard.writeText(lastAgentText);
          return notify("copied last response");
        case "/remember":
          return void forge
            .remember(arg)
            .then(notify)
            .catch((e: unknown) => notify(String(e)));
        default:
          return sendInput(raw);
      }
    },
    [
      cancel,
      lastAgentText,
      newThread,
      notify,
      sendInput,
      setEffortAction,
      setTheme,
      switchModel,
      theme,
      toggleYolo,
      yolo,
    ],
  );

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const mod = e.metaKey || e.ctrlKey;
      if (e.key === "Escape") {
        if (overlay !== "none") return setOverlay("none");
        if (busy) return cancel();
        return;
      }
      if (!mod) return;
      const k = e.key.toLowerCase();
      // Zoom shortcuts, as in any desktop app. "=" is the unshifted "+".
      if (k === "=" || k === "+") {
        e.preventDefault();
        return setScale(step(scaleRef.current, 1));
      }
      if (k === "-" || k === "_") {
        e.preventDefault();
        return setScale(step(scaleRef.current, -1));
      }
      if (k === "0") {
        e.preventDefault();
        return setScale(DEFAULT_SCALE);
      }
      const map: Record<string, () => void> = {
        k: () => setOverlay((o) => (o === "models" ? "none" : "models")),
        ",": () => setOverlay((o) => (o === "settings" ? "none" : "settings")),
        o: () =>
          setOverlay((o) => (o === "workspaces" ? "none" : "workspaces")),
        n: newThread,
        b: () => setPrefsState((p) => ({ ...p, showSidebar: !p.showSidebar })),
      };
      const fn = map[k];
      if (fn) {
        e.preventDefault();
        fn();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [busy, cancel, newThread, overlay, setScale]);

  // Images are read here and handed to Go as base64; the runtime loads
  // attachments from disk, so the backend writes them out to a temp file.
  const attachFiles = useCallback(
    async (files: File[]) => {
      for (const file of files) {
        if (!file.type.startsWith("image/")) {
          notify(`${file.name}: not an image`);
          continue;
        }
        try {
          const b64 = await new Promise<string>((resolve, reject) => {
            const reader = new FileReader();
            reader.onload = () => resolve(String(reader.result));
            reader.onerror = () => reject(reader.error);
            reader.readAsDataURL(file);
          });
          const att = await forge.attachImage(file.name, b64);
          setPending((p) => [...p, att]);
        } catch (e) {
          notify(`${file.name}: ${String(e)}`);
        }
      }
    },
    [notify],
  );

  // A webview may hand over a dragged file either as a File or as a file://
  // URI. Finder drags commonly take the second route, which left the drop
  // looking accepted while nothing attached.
  const attachDrop = useCallback(
    async (data: DataTransfer) => {
      const files = Array.from(data.files);
      if (files.length > 0) {
        await attachFiles(files);
        return;
      }
      const uris = (
        data.getData("text/uri-list") ||
        data.getData("text/plain") ||
        ""
      )
        .split(/[\r\n]+/)
        .map((line) => line.trim())
        .filter((line) => line.startsWith("file://") || line.startsWith("/"));
      if (uris.length === 0) {
        notify("nothing attachable in that drop");
        return;
      }
      await attachPaths(uris);
    },
    [attachFiles, attachPaths, notify],
  );

  const isFileDrag = (data: DataTransfer) =>
    data.files.length > 0 ||
    data.types.includes("Files") ||
    data.types.includes("text/uri-list");

  return (
    <div
      className={`app ${dragging ? "dragging" : ""}`}
      data-file-drop-target
      onDragEnter={(e) => {
        if (!isFileDrag(e.dataTransfer)) return;
        e.preventDefault();
        dragDepth.current++;
        setDragging(true);
      }}
      onDragOver={(e) => {
        if (isFileDrag(e.dataTransfer)) e.preventDefault();
      }}
      onDragLeave={(e) => {
        if (!isFileDrag(e.dataTransfer)) return;
        e.preventDefault();
        if (--dragDepth.current <= 0) setDragging(false);
      }}
      onDrop={(e) => {
        if (!isFileDrag(e.dataTransfer)) return;
        e.preventDefault();
        dragDepth.current = 0;
        setDragging(false);
        void attachDrop(e.dataTransfer);
      }}
    >
      {flash ? <span className="flash app-flash">{flash}</span> : null}

      {/* Startup decided something the user did not ask for — most often that
          the saved model was dropped because no provider serves it any more.
          A 2.6s flash was long enough to miss, which left an empty model pill
          with nothing to explain it. */}
      {init?.notice && !noticeSeen ? (
        <div className="notice">
          <span>{init.notice}</span>
          <button className="notice-act" onClick={() => setOverlay("models")}>
            Pick a model
          </button>
          <button
            className="notice-x"
            onClick={() => setNoticeSeen(true)}
            aria-label="Dismiss"
          >
            ×
          </button>
        </div>
      ) : null}

      <div
        className="cols"
        style={
          { "--sidebar-width": `${sidebarWidth}px` } as React.CSSProperties
        }
      >
        {prefs.showSidebar ? (
          <>
            <Sidebar
              threads={threads}
              workspaces={workspaces}
              workDir={init?.work_dir ?? ""}
              activeID={activeID}
              busy={busy}
              busyWorkspaces={busyDirs}
              terminalWorkspaces={terminalDirs}
              onNew={newThread}
              onRestore={restoreThread}
              onOpenThread={openExactThread}
              onAddWorkspace={addWorkspace}
              onNewIn={newSessionIn}
              onOpenWorkspace={openWorkspace}
              onDelete={deleteThread}
              onRename={(id, title) =>
                void forge
                  .renameThread(id, title)
                  .then(setThreads)
                  .catch((e: unknown) => notify(String(e)))
              }
              onPin={(dir, pinned) =>
                void forge
                  .pinWorkspace(dir, pinned)
                  .then(setWorkspaces)
                  .catch((e: unknown) => notify(String(e)))
              }
              onForget={closeWorkspace}
              onBrowse={browseWorkspace}
              browsing={browseDir || (init?.work_dir ?? "")}
              onBulkDelete={bulkDelete}
              onClearThreads={clearThreads}
              openThreadIDs={openThreadIDs}
              onCloseThread={closeChat}
              threadStatus={threadStatus}
              threadAttention={threadAttention}
            />
            <div
              aria-label="Resize workspace panel"
              aria-orientation="vertical"
              aria-valuenow={Math.round(sidebarWidth)}
              className="sidebar-divider"
              onDoubleClick={() => setSidebarWidth(DEFAULT_SIDEBAR_WIDTH)}
              onKeyDown={resizeSidebarWithKeys}
              onPointerDown={startSidebarDrag}
              role="separator"
              tabIndex={0}
            />
          </>
        ) : null}
        <WorkspaceShell
          key={`${browseDir || (init?.work_dir ?? "")}:${prefs.dockLayoutPersistence}`}
          workDir={browseDir || (init?.work_dir ?? "")}
          browsing={browseDir}
          chatDir={init?.work_dir ?? ""}
          onWorkHere={(dir) => openWorkspace(dir)}
          onStopBrowsing={() => browseWorkspace(init?.work_dir ?? "")}
          active={workspaceMode}
          onDirtyChange={setWorkspaceDirty}
          onNotify={notify}
          onTerminalPresenceChange={setTerminalPresence}
          onTerminalPanelClose={closeTerminalPanel}
          onShowDocks={() => setWorkspaceMode(true)}
          showActivity={prefs.showActivity}
          onActivityClosed={() =>
            setPrefsState((p) => ({ ...p, showActivity: false }))
          }
          activity={
            <ActivityPanel
              entries={entries}
              stats={stats}
              subAgents={subAgents}
            />
          }
          model={init?.model ?? ""}
          models={init?.models ?? []}
          layoutPersistence={prefs.dockLayoutPersistence}
        >
          <main className="center">
            <Transcript entries={entries} prefs={prefs} busy={busy} />
            <Composer
              draft={drafts[chatKey] ?? ""}
              onDraftChange={(draft) =>
                setDrafts((current) => ({ ...current, [chatKey]: draft }))
              }
              yolo={yolo}
              onToggleYolo={() => toggleYolo(!yolo)}
              model={stats.model ?? ""}
              onModel={() => setOverlay("models")}
              effort={effort}
              efforts={init?.efforts ?? []}
              onEffort={setEffortAction}
              busy={busy}
              skills={init?.skills ?? []}
              commands={init?.commands ?? []}
              history={history}
              attachments={pending}
              onRemoveAttachment={(id) =>
                setPending((p) => p.filter((a) => a.id !== id))
              }
              onFiles={(files) => void attachFiles(files)}
              onSend={sendInput}
              onCancel={cancel}
              onCommand={runCommand}
              focusToken={composerFocus}
            />
          </main>
        </WorkspaceShell>
      </div>

      <StatsBar
        stats={stats}
        connected={init !== null}
        actions={
          <>
            <button
              className="icon-btn"
              onClick={() =>
                setPrefsState((p) => ({ ...p, showSidebar: !p.showSidebar }))
              }
              title="Toggle sidebar (⌘B)"
            >
              ☰
            </button>
            <span className="topbar-menu-slot" id="forge-terminal-button" />
            <button
              className={`icon-btn ${workspaceMode ? "on" : ""}`}
              onClick={() => setWorkspaceMode((on) => !on)}
              title={workspaceMode ? "Hide all docks" : "Show all docks"}
              aria-label={workspaceMode ? "Hide all docks" : "Show all docks"}
            >
              ◫
            </button>
            <button
              className="icon-btn"
              onClick={() => setOverlay("settings")}
              title="Settings (⌘,)"
            >
              ⚙
            </button>
          </>
        }
      />

      {dragging ? <div className="drop-veil">drop images to attach</div> : null}

      {approval ? (
        <ApprovalModal
          action={approval}
          pendingCount={approvalQueue.length}
          submitting={submittingApproval}
          onApprove={() => approve(true)}
          onDeny={() => approve(false)}
        />
      ) : null}
      {overlay === "models" && init ? (
        <ModelPicker
          models={init.models}
          current={stats.model ?? ""}
          onPick={switchModel}
          onClose={() => setOverlay("none")}
        />
      ) : null}
      {overlay === "workspaces" ? (
        <WorkspaceMenu
          workspaces={workspaces}
          busyWorkspaces={busyDirs}
          onNewIn={newSessionIn}
          onOpen={(dir) => {
            setOverlay("none");
            openWorkspace(dir);
          }}
          onAdd={() => {
            setOverlay("none");
            addWorkspace();
          }}
          onClose={() => setOverlay("none")}
        />
      ) : null}
      {overlay === "settings" ? (
        <SettingsPanel
          init={init}
          model={stats.model ?? ""}
          effort={effort}
          theme={theme}
          scale={scale}
          vividness={vividness}
          prefs={prefs}
          onTheme={setTheme}
          onScale={setScale}
          onVividness={setVividness}
          onModel={() => setOverlay("models")}
          onEffort={setEffortAction}
          onPrefs={(next) => {
            setPrefsState(next);
            if (next.yolo !== prefs.yolo) {
              toggleYolo(next.yolo);
            }
            if (next.showDocks !== prefs.showDocks) {
              setWorkspaceMode(next.showDocks);
            }
          }}
          onProviders={(next) =>
            setInit((i) => (i ? { ...i, providers: next } : i))
          }
          onSkills={(next) => setInit((i) => (i ? { ...i, skills: next } : i))}
          onAddWorkspace={() => {
            setOverlay("none");
            addWorkspace();
          }}
          onOpenWorkspaces={() => setOverlay("workspaces")}
          onNotify={notify}
          onClose={() => setOverlay("none")}
        />
      ) : null}
      {overlay === "help" ? (
        <HelpOverlay onClose={() => setOverlay("none")} />
      ) : null}
    </div>
  );
}
