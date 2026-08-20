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
import { Transcript } from "./components/Transcript";
import { ApprovalModal } from "./components/ApprovalModal";
import { Composer } from "./components/Composer";
import { StatsBar, type Stats } from "./components/StatsBar";
import { ActivityPanel } from "./components/ActivityPanel";
import { ModelPicker } from "./components/ModelPicker";
import { SettingsPanel, type Prefs } from "./components/SettingsPanel";
import { HelpOverlay } from "./components/HelpOverlay";
import { WorkspaceMenu } from "./components/WorkspaceMenu";
import { applyTheme, isTheme, loadTheme, nextTheme, type Theme } from "./theme";
import { applyScale, clampScale, formatScale, loadScale, step, DEFAULT_SCALE } from "./scale";

const initialStats: Stats = {
  inTok: 0,
  outTok: 0,
  contextUsed: 0,
  contextLimit: 0,
  durationMs: 0,
  model: "",
};

const defaultPrefs: Prefs = {
  showTools: true,
  showReasoning: true,
  expandReasoning: true,
  showActivity: true,
  showSidebar: true,
  scopeThreads: true,
};

function loadPrefs(): Prefs {
  try {
    return { ...defaultPrefs, ...JSON.parse(localStorage.getItem("forge.prefs") ?? "{}") };
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
  const scaleRef = useRef(scale);
  scaleRef.current = scale;
  const dragDepth = useRef(0);
  const [dragging, setDragging] = useState(false);

  useEffect(() => applyTheme(theme), [theme]);
  useEffect(() => applyScale(scale), [scale]);
  useEffect(() => localStorage.setItem("forge.prefs", JSON.stringify(prefs)), [prefs]);

  const notify = useCallback((msg: string) => {
    setFlash(msg);
    setTimeout(() => setFlash(""), 2600);
  }, []);

  const refreshThreads = useCallback(() => {
    void forge.threads().then(setThreads);
    void forge.workspaces().then(setWorkspaces);
  }, []);

  const loadInit = useCallback(() => {
    void forge.init().then((payload) => {
      if (!payload.ready) return;
      setInit(payload);
      setStats((s) => ({ ...s, model: payload.model }));
      if (payload.effort) setEffort(payload.effort);
      setYoloState(payload.yolo);
      if (payload.thread_id) setActiveID(payload.thread_id);
      refreshThreads();
    });
  }, [refreshThreads]);

  useEffect(() => {
    loadInit();
    const offReady = forge.onReady(loadInit);
    const offEvent = forge.onEvent((ev) => {
      setEntries((prev) => applyEvent(prev, ev));
      if (ev.kind === "stats" && ev.usage) {
        const u = ev.usage;
        setStats((s) => ({
          ...s,
          inTok: s.inTok + u.input_tokens,
          outTok: s.outTok + u.output_tokens,
        }));
      }
      if (ev.context_used || ev.context_limit || ev.duration_ms) {
        setStats((s) => ({
          ...s,
          contextUsed: ev.context_used || s.contextUsed,
          contextLimit: ev.context_limit || s.contextLimit,
          durationMs: ev.duration_ms || s.durationMs,
        }));
      }
      if (ev.kind === "done" || ev.kind === "agent_done" || ev.kind === "abort") {
        setBusy(false);
        refreshThreads();
      }
    });
    const offApproval = forge.onApproval(setApproval);
    const offDone = forge.onTurnDone(() => {
      setBusy(false);
      refreshThreads();
    });
    return () => {
      offReady();
      offEvent();
      offApproval();
      offDone();
    };
  }, [loadInit, refreshThreads]);

  const sendInput = useCallback(
    (text: string) => {
      setEntries((prev) => [...prev, userEntry(text)]);
      setHistory((h) => [...h, text]);
      setBusy(true);
      const images = pending;
      setPending([]);
      const p = images.length > 0 ? forge.sendWithImages(text, images) : forge.send(text);
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
  };

  const cancel = useCallback(() => void forge.cancel(), []);
  const newThread = useCallback(() => {
    void forge.newSession().then(() => {
      setEntries([]);
      setActiveID("");
      refreshThreads();
    });
  }, [refreshThreads]);

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
          notify(now ? "yolo on — tools run without asking" : "yolo off — tools ask first");
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
      setEntries([]);
      setActiveID("");
      setBusy(false);
      notify(`opening ${dir.split("/").pop()}…`);
      void forge.switchWorkspace(dir).catch((e: unknown) => notify(String(e)));
    },
    [notify],
  );

  const addWorkspace = useCallback(() => {
    void forge
      .chooseWorkspace()
      .then((dir) => {
        if (dir) {
          setEntries([]);
          setActiveID("");
        }
      })
      .catch((e: unknown) => notify(String(e)));
  }, [notify]);

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
          return setPrefsState((p) => ({ ...p, showActivity: !p.showActivity }));
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
          return toggleYolo(arg === "" ? !yolo : arg === "on" || arg === "true");
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
    [cancel, lastAgentText, newThread, notify, sendInput, setEffortAction, setTheme, switchModel, theme, toggleYolo, yolo],
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
        o: () => setOverlay((o) => (o === "workspaces" ? "none" : "workspaces")),
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

  const workDirLabel = init?.work_dir?.replace(/^\/Users\/[^/]+/, "~") ?? "";

  return (
    <div
      className={`app ${dragging ? "dragging" : ""}`}
      onDragEnter={(e) => {
        e.preventDefault();
        dragDepth.current++;
        setDragging(true);
      }}
      onDragOver={(e) => e.preventDefault()}
      onDragLeave={(e) => {
        e.preventDefault();
        if (--dragDepth.current <= 0) setDragging(false);
      }}
      onDrop={(e) => {
        e.preventDefault();
        dragDepth.current = 0;
        setDragging(false);
        void attachFiles(Array.from(e.dataTransfer.files));
      }}
    >
      <header className="topbar">
        <button
          className="icon-btn"
          onClick={() => setPrefsState((p) => ({ ...p, showSidebar: !p.showSidebar }))}
          title="Toggle sidebar (⌘B)"
        >
          ☰
        </button>
        <span className="brand">FORGE</span>
        <button className="workspace-btn" onClick={() => setOverlay("workspaces")} title="Switch workspace (⌘O)">
          <span className="ws-name">{init?.work_dir ? init.work_dir.split("/").pop() : "no workspace"}</span>
          <span className="ws-path">{workDirLabel}</span>
        </button>
        <span className="topbar-spacer" />
        {flash ? <span className="flash">{flash}</span> : null}
        <button className="pill" onClick={() => setOverlay("models")} title="Switch model (⌘K)">
          {stats.model || "—"}
        </button>
        {init && init.efforts && init.efforts.length > 0 ? (
          <div className="seg">
            {init.efforts.map((e) => (
              <button key={e} className={`seg-btn ${e === effort ? "on" : ""}`} onClick={() => setEffortAction(e)}>
                {e}
              </button>
            ))}
          </div>
        ) : null}
        <button className="icon-btn" onClick={() => setOverlay("settings")} title="Settings (⌘,)">
          ⚙
        </button>
      </header>

      <div className="cols">
        {prefs.showSidebar ? (
          <Sidebar
            threads={threads}
            workspaces={workspaces}
            workDir={init?.work_dir ?? ""}
            activeID={activeID}
            busy={busy}
            onNew={newThread}
            onRestore={restoreThread}
            onAddWorkspace={addWorkspace}
            onOpenWorkspace={openWorkspace}
            onDelete={deleteThread}
            onPin={(dir, pinned) =>
              void forge.pinWorkspace(dir, pinned).then(setWorkspaces).catch((e: unknown) => notify(String(e)))
            }
            onForget={(dir) =>
              void forge.forgetWorkspace(dir).then(setWorkspaces).catch((e: unknown) => notify(String(e)))
            }
          />
        ) : null}
        <main className="center">
          <Transcript entries={entries} prefs={prefs} busy={busy} />
          <Composer
            yolo={yolo}
            onToggleYolo={() => toggleYolo(!yolo)}
            busy={busy}
            skills={init?.skills ?? []}
            history={history}
            attachments={pending}
            onRemoveAttachment={(id) => setPending((p) => p.filter((a) => a.id !== id))}
            onFiles={(files) => void attachFiles(files)}
            onSend={sendInput}
            onCancel={cancel}
            onCommand={runCommand}
          />
        </main>
        {prefs.showActivity ? <ActivityPanel entries={entries} stats={stats} /> : null}
      </div>

      <StatsBar stats={stats} connected={init !== null} />

      {dragging ? <div className="drop-veil">drop images to attach</div> : null}

      {approval ? (
        <ApprovalModal action={approval} onApprove={() => approve(true)} onDeny={() => approve(false)} />
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
          onProviders={(next) => setInit((i) => (i ? { ...i, providers: next } : i))}
          onAddWorkspace={() => {
            setOverlay("none");
            addWorkspace();
          }}
          onOpenWorkspaces={() => setOverlay("workspaces")}
          onNotify={notify}
          onClose={() => setOverlay("none")}
        />
      ) : null}
      {overlay === "help" ? <HelpOverlay onClose={() => setOverlay("none")} /> : null}
    </div>
  );
}
