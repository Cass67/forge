import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  forge,
  type InitPayload,
  type ThreadSummary,
  type WireAction,
  type Workspace,
  type Attachment,
} from "./bridge";
import { applyEvent, userEntry, type Entry } from "./entries";
import { itemsToEntries } from "./replay";
import { Sidebar } from "./components/Sidebar";
import { SessionTabs } from "./components/SessionTabs";
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
  const [approval, setApproval] = useState<WireAction | null>(null);
  const [stats, setStats] = useState<Stats>(initialStats);
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
  const switchingWorkspace = useRef(false);
  const scaleRef = useRef(scale);
  scaleRef.current = scale;
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
  // Set when a new session was asked for in a workspace that is not open yet:
  // the switch tears the runtime down, so the request is replayed once the
  // rebuilt one reports ready.
  const pendingNewSession = useRef(false);
  const newThreadRef = useRef<() => void>(() => {});

  const markRef = useRef(markActive);
  markRef.current = markActive;
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
      switchingWorkspace.current = false;
      setInit(payload);
      setStats((s) => ({ ...s, model: payload.model }));
      if (payload.effort) setEffort(payload.effort);
      setYoloState(payload.yolo);
      if (payload.thread_id) {
        setActiveID(payload.thread_id);
        void forge
          .history(payload.thread_id)
          .then((items) => setEntries(itemsToEntries(items)));
      } else {
        setActiveID("");
        setEntries([]);
      }
      refreshThreads();
      if (pendingNewSession.current) {
        pendingNewSession.current = false;
        newThreadRef.current();
      }
    });
  }, [refreshThreads]);

  useEffect(() => {
    loadInit();
    const offReady = forge.onReady(loadInit);
    const offEvent = forge.onEvent((ev) => {
      if (switchingWorkspace.current) return;
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
      if (
        ev.kind === "done" ||
        ev.kind === "agent_done" ||
        ev.kind === "abort"
      ) {
        setBusy(false);
        markRef.current(turnFailed.current ? "failed" : "done");
        refreshThreads();
      }
    });
    // The webview never gives the DOM a dragged file; Wails delivers the
    // paths here instead.
    const offFiles = forge.onFilesDropped((paths) => {
      setDragging(false);
      dragDepth.current = 0;
      void attachPaths(paths);
    });
    const offApproval = forge.onApproval((action) => {
      // An approval from the runtime being torn down belongs to a workspace
      // that is no longer on screen; answering it would target the wrong one.
      if (switchingWorkspace.current) return;
      setApproval(action);
      markRef.current("waiting");
    });
    const offDone = forge.onTurnDone(() => {
      setBusy(false);
      markRef.current(turnFailed.current ? "failed" : "done");
      refreshThreads();
    });
    return () => {
      offReady();
      offEvent();
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
    void forge.newSession().then(() => {
      setEntries([]);
      setActiveID("");
      refreshThreads();
    });
  }, [refreshThreads]);
  newThreadRef.current = newThread;

  const restoreThread = useCallback(
    (id: string) => {
      void forge
        .restore(id)
        .then((r) => {
          setEntries(itemsToEntries(r.items));
          setActiveID(r.thread_id);
          refreshThreads();
        })
        .catch((e: unknown) => notify(String(e)));
    },
    [notify, refreshThreads],
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

  // Switching rebuilds the runtime in this window; the frontend re-initialises
  // when the backend signals it is ready again.
  const openWorkspace = useCallback(
    (dir: string) => {
      if (
        workspaceDirty &&
        !window.confirm(
          "Discard unsaved workspace changes and switch workspaces?",
        )
      )
        return;
      setEntries([]);
      setActiveID("");
      setBusy(false);
      setApproval(null);
      setPending([]);
      switchingWorkspace.current = true;
      notify(`opening ${dir.split("/").pop()}…`);
      void forge.switchWorkspace(dir).catch((e: unknown) => notify(String(e)));
    },
    [notify, workspaceDirty],
  );

  // Double-clicking a workspace starts a fresh session in it. When it is not
  // the workspace already open, the switch has to land first — openWorkspace
  // tears the runtime down — so the request is deferred to loadInit.
  const newSessionIn = useCallback(
    (dir: string) => {
      if (!dir || dir === (init?.work_dir ?? "")) {
        newThread();
        return;
      }
      pendingNewSession.current = true;
      openWorkspace(dir);
    },
    [init?.work_dir, newThread, openWorkspace],
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
      .then((dir) => {
        if (dir) {
          setEntries([]);
          setActiveID("");
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
              onForget={(dir) =>
                void forge
                  .forgetWorkspace(dir)
                  .then(setWorkspaces)
                  .catch((e: unknown) => notify(String(e)))
              }
              onBulkDelete={bulkDelete}
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
          model={init?.model ?? ""}
          models={init?.models ?? []}
        >
          <main className="center">
            <SessionTabs
              tabs={tabs}
              threads={threads}
              activeID={activeID}
              onSelect={(id) => {
                if (id !== activeID) restoreThread(id);
              }}
              onClose={(id) => {
                const next = nextAfterClose(tabs, id);
                setTabs((current) => closeSessionTab(current, id));
                if (id === activeID) {
                  if (next) restoreThread(next);
                  else newThread();
                }
              }}
              onNew={newThread}
            />
            <Transcript entries={entries} prefs={prefs} busy={busy} />
            <Composer
              draft={drafts[init?.work_dir ?? ""] ?? ""}
              onDraftChange={(draft) =>
                setDrafts((current) => ({
                  ...current,
                  [init?.work_dir ?? ""]: draft,
                }))
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
            />
          </main>
          {prefs.showActivity ? (
            <ActivityPanel entries={entries} stats={stats} />
          ) : null}
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
