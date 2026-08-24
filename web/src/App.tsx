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
import { StatsBar, type Stats } from "./components/StatsBar";
import { ActivityPanel } from "./components/ActivityPanel";
import { ModelPicker } from "./components/ModelPicker";
import { SettingsPanel, type Prefs } from "./components/SettingsPanel";
import { HelpOverlay } from "./components/HelpOverlay";
import { WorkspaceMenu } from "./components/WorkspaceMenu";
import { WorkspaceShell } from "./components/WorkspaceShell";
import { applyTheme, isTheme, loadTheme, nextTheme, type Theme } from "./theme";
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

const initialStats: Stats = {
  inTok: 0,
  outTok: 0,
  cachedTok: 0,
  lastOut: 0,
  lastMs: 0,
  contextUsed: 0,
  contextLimit: 0,
  durationMs: 0,
  model: "",
};

const defaultPrefs: Prefs = {
  showTools: true,
  showReasoning: true,
  expandReasoning: true,
  expandTools: false,
  showActivity: true,
  showSidebar: true,
  scopeThreads: true,
  expandSubfolders: false,
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
  const [approval, setApproval] = useState<WireAction | null>(null);
  const [stats, setStats] = useState<Stats>(initialStats);
  const [noticeSeen, setNoticeSeen] = useState(false);
  const [effort, setEffort] = useState("");
  const [yolo, setYoloState] = useState(false);
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
  // Sub-agent roles that have produced output in the turn on screen. Cleared
  // when the turn ends, which is the only signal the event stream gives that
  // delegation is over.
  const [subAgents, setSubAgents] = useState<string[]>([]);
  // Which live sessions have a turn in flight. A background session keeps
  // working, so this is what the sidebar dots read.
  const [busySessions, setBusySessions] = useState<Record<string, boolean>>({});
  // Every live session, from the backend. Several can share a workspace.
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  // Transcripts stashed per session, so going to another conversation and back
  // restores what it streamed instead of replaying it from storage.
  const sessionCache = useRef(
    new Map<string, { entries: Entry[]; activeID: string }>(),
  );
  const prevSessionRef = useRef("");
  // Approvals that arrived while their session was in the background; they are
  // shown when that session comes back on screen.
  const pendingApprovals = useRef<Record<string, WireAction>>({});
  const switchingWorkspace = useRef(false);
  const scaleRef = useRef(scale);
  scaleRef.current = scale;
  const prefsRef = useRef(prefs);
  prefsRef.current = prefs;
  const dragDepth = useRef(0);
  const [dragging, setDragging] = useState(false);
  const [workspaceMode, setWorkspaceMode] = useState(false);
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
    void forge.workspaces().then(setWorkspaces);
  }, []);

  const loadInit = useCallback(() => {
    void forge.init().then((payload) => {
      if (!payload.ready) return;
      const session = payload.session ?? "";
      switchingWorkspace.current = false;
      setInit(payload);
      setNoticeSeen(false);
      setStats((s) => ({ ...s, model: payload.model }));
      if (payload.effort) setEffort(payload.effort);
      setYoloState(payload.yolo);
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
        setSubAgents([]);
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
      // An approval that arrived while this session was in the background is
      // still waiting on an answer.
      const queued = pendingApprovals.current[session];
      if (queued) {
        delete pendingApprovals.current[session];
        setApproval(queued);
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
      // Background sessions keep streaming into their own caches: their
      // output must never land in the transcript on screen.
      if (from && from !== sessionRef.current) {
        cacheBackground(from, ev);
        return;
      }
      if (switchingWorkspace.current) return;
      if (ev.sub_agent) {
        const role = ev.sub_agent;
        setSubAgents((roles) =>
          roles.includes(role) ? roles : [...roles, role],
        );
      }
      setEntries((prev) => applyEvent(prev, ev));
      if (ev.is_error || ev.error) turnFailed.current = true;
      if (ev.kind === "stats" && ev.usage) {
        const u = ev.usage;
        setStats((s) => ({
          ...s,
          inTok: s.inTok + u.input_tokens,
          outTok: s.outTok + u.output_tokens,
          cachedTok: s.cachedTok + (u.cached_input_tokens || 0),
          // Kept separately so the rate describes the last turn rather than
          // an average dragged down by time spent idle.
          lastOut: u.output_tokens || s.lastOut,
        }));
      }
      if (ev.context_used || ev.context_limit || ev.duration_ms) {
        setStats((s) => ({
          ...s,
          contextUsed: ev.context_used || s.contextUsed,
          contextLimit: ev.context_limit || s.contextLimit,
          durationMs: ev.duration_ms || s.durationMs,
          lastMs: ev.duration_ms || s.lastMs,
        }));
      }
      if (finished) {
        setBusy(false);
        setSubAgents([]);
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
      // An approval from a background session stays queued against that
      // session: answering it here would hit whichever one is on screen.
      const from = action.session ?? "";
      if (from && from !== sessionRef.current) {
        pendingApprovals.current[from] = action;
        setBusySessions((m) => flag(m, from, true));
        if (action.workspace)
          setBusyDirs((m) => flag(m, action.workspace as string, true));
        return;
      }
      if (switchingWorkspace.current) return;
      setApproval(action);
      markRef.current("waiting");
    });
    const offDone = forge.onTurnDone((done) => {
      const from = done.session ?? "";
      const tag = done.workspace ?? "";
      if (from) setBusySessions((m) => flag(m, from, false));
      if (tag) setBusyDirs((m) => flag(m, tag, false));
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
    void forge.approve(ok);
    setApproval(null);
    markRef.current("working");
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
          refreshThreads();
          void forge.sessions().then(setSessions);
        })
        .catch((e: unknown) => notify(String(e)));
    },
    [notify, refreshThreads],
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

  // A thread is open if it has a live session, whatever this window remembers.
  const openThreadIDs = useMemo(() => {
    const ids = new Set(tabs.open);
    for (const session of sessions) {
      if (session.thread_id) ids.add(session.thread_id);
    }
    return [...ids];
  }, [sessions, tabs.open]);

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
        let failed = 0;
        for (const id of threadIDs) {
          try {
            setThreads(await forge.deleteThread(id));
          } catch {
            failed++;
          }
        }
        for (const dir of dirs) {
          try {
            setWorkspaces(await forge.forgetWorkspace(dir));
          } catch {
            failed++;
          }
        }
        const removed = threadIDs.length + dirs.length - failed;
        notify(
          failed
            ? `removed ${removed}, ${failed} failed`
            : `removed ${removed} item${removed === 1 ? "" : "s"}`,
        );
      })();
    },
    [notify],
  );

  const switchModel = useCallback(
    (model: string) => {
      setOverlay("none");
      void forge
        .switchModel(model)
        .then((applied) => {
          setStats((s) => ({ ...s, model: applied }));
          setNoticeSeen(true);
          notify(`model → ${applied}`);
          return forge.efforts(applied);
        })
        .then((list) => setInit((i) => (i ? { ...i, efforts: list } : i)))
        .catch((e: unknown) => notify(String(e)));
    },
    [notify],
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

  const workDirLabel = init?.work_dir?.replace(/^\/Users\/[^/]+/, "~") ?? "";

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
      <header className="topbar">
        <button
          className="icon-btn"
          onClick={() =>
            setPrefsState((p) => ({ ...p, showSidebar: !p.showSidebar }))
          }
          title="Toggle sidebar (⌘B)"
        >
          ☰
        </button>
        <span className="brand">FORGE</span>
        <button
          className="workspace-btn"
          onClick={() => setOverlay("workspaces")}
          title="Switch workspace (⌘O)"
        >
          <span className="ws-name">
            {init?.work_dir ? init.work_dir.split("/").pop() : "no workspace"}
          </span>
          <span className="ws-path">{workDirLabel}</span>
        </button>
        {/* The panels menu is rendered here by the workspace shell, which owns
            the dock layout. A dropdown inside a panel's own tab strip was
            clipped by the strip's scroll box, and there was nowhere obvious to
            look for it. */}
        <span className="topbar-menu-slot" id="forge-panel-menu" />
        <span className="topbar-spacer" />
        {flash ? <span className="flash">{flash}</span> : null}
        <button
          className="pill"
          onClick={() => setOverlay("models")}
          title="Switch model (⌘K)"
        >
          {stats.model || "—"}
        </button>
        <button
          className={`pill ${workspaceMode ? "on" : ""}`}
          onClick={() => setWorkspaceMode((on) => !on)}
        >
          {workspaceMode ? "Hide Docks" : "Show Docks"}
        </button>
        {init && init.efforts && init.efforts.length > 0 ? (
          <div className="seg">
            {init.efforts.map((e) => (
              <button
                key={e}
                className={`seg-btn ${e === effort ? "on" : ""}`}
                onClick={() => setEffortAction(e)}
              >
                {e}
              </button>
            ))}
          </div>
        ) : null}
        <button
          className="icon-btn"
          onClick={() => setOverlay("settings")}
          title="Settings (⌘,)"
        >
          ⚙
        </button>
      </header>

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
              onNew={newThread}
              onRestore={restoreThread}
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
              onBulkDelete={bulkDelete}
              onClearThreads={clearThreads}
              openThreadIDs={openThreadIDs}
              onCloseThread={closeChat}
              threadStatus={threadStatus}
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
          workDir={init?.work_dir ?? ""}
          active={workspaceMode}
          onDirtyChange={setWorkspaceDirty}
          onNotify={notify}
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
              busy={busy}
              skills={init?.skills ?? []}
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

      <StatsBar stats={stats} connected={init !== null} />

      {dragging ? <div className="drop-veil">drop images to attach</div> : null}

      {approval ? (
        <ApprovalModal
          action={approval}
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
          prefs={prefs}
          onTheme={setTheme}
          onScale={setScale}
          onModel={() => setOverlay("models")}
          onEffort={setEffortAction}
          onPrefs={setPrefsState}
          onProviders={(next) =>
            setInit((i) => (i ? { ...i, providers: next } : i))
          }
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
