import {
  Fragment,
  type ReactNode,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import {
  forge,
  type GitStatusResult,
  type WorkspaceEntry,
  type WorkspaceFile,
} from "../bridge";
import {
  ACTIVITY_TOOL,
  addTool,
  allTools,
  clampDock,
  columnLayout,
  DEFAULT_DOCK_WIDTHS,
  type DockColumns,
  type DockGroup,
  type DockLayoutPersistence,
  type DockSide,
  type DockTool,
  dockFraction,
  dockStorageKeys,
  type DockWidths,
  type DropTarget,
  dropZone,
  findTool,
  loadColumns,
  loadDockWidths,
  moveTool,
  nextTerminalNumber,
  removeTool,
  resizeGroups,
  saveColumns,
  saveDockWidths,
  setActiveTool,
  showsDivider,
  SIDES,
} from "../dockLayout";
import {
  acceptSavedFile,
  filterPaths,
  isDirty,
  type OpenFile,
} from "../workspaceFiles";
import type { GitTab } from "../gitTabs";
import { CodeEditor } from "./CodeEditor";
import { GitTabView } from "./DiffView";
import { GitPanel } from "./GitPanel";
import { MultiRunDialog } from "./MultiRunDialog";
import { PreviewPanel } from "./PreviewPanel";
import { TerminalWorkspace } from "./TerminalWorkspace";

type Props = {
  workDir: string;
  active: boolean;
  children: ReactNode;
  // The progress panel's contents. It lives in the dock like any other panel,
  // so the shell places it and the chat owns neither its position nor its size.
  activity: ReactNode;
  // Set when the panels are pointed at a workspace the chat is not in. That
  // is read-only: the service refuses every mutation, so the panels say so
  // rather than offering buttons that fail.
  browsing: string;
  chatDir: string;
  onWorkHere: (dir: string) => void;
  onStopBrowsing: () => void;
  // Whether the progress panel should be docked. The settings toggle and the
  // panel's own ✕ are the same switch seen from two places, so closing it
  // reports back rather than leaving the setting claiming it is open.
  showActivity: boolean;
  onActivityClosed: () => void;
  onDirtyChange: (dirty: boolean) => void;
  onNotify: (message: string) => void;
  // Opening a panel from the menu while the docks are hidden has to show them
  // again, or the click looks like it did nothing.
  onShowDocks: () => void;
  // The chat's current model and the models it can switch to: the source
  // control panel drafts commit messages with the former and multi-run
  // launches windows across the latter.
  model: string;
  models: string[];
  layoutPersistence: DockLayoutPersistence;
};
type TreeNode = WorkspaceEntry & {
  loaded?: boolean;
  expanded?: boolean;
  children?: TreeNode[];
};
const DOCK_TOOL_MIME = "application/x-forge-dock-tool";
// The two columns whose width the user can drag; the chat column takes what is
// left over.
type EdgeSide = "left" | "right";

// Every tool lives in a host element that is moved between group bodies rather
// than re-rendered into them: a terminal keeps its scrollback and the chat
// keeps its scroll position when its tab is dragged to another panel.
function DockToolHost({
  target,
  active,
  kind,
  children,
}: {
  target: HTMLDivElement | null;
  active: boolean;
  kind: string;
  children: ReactNode;
}) {
  const [host] = useState(() => document.createElement("div"));

  useLayoutEffect(() => {
    host.className = `workspace-tool workspace-tool-${kind} ${active ? "active" : ""}`;
    target?.appendChild(host);
  }, [active, host, kind, target]);

  useEffect(() => () => host.remove(), [host]);
  return target ? createPortal(children, host) : null;
}

function patchTree(
  nodes: TreeNode[],
  path: string,
  patch: Partial<TreeNode>,
): TreeNode[] {
  return nodes.map((node) =>
    node.path === path
      ? { ...node, ...patch }
      : node.children
        ? { ...node, children: patchTree(node.children, path, patch) }
        : node,
  );
}

function FileTree({
  nodes,
  active,
  onOpen,
  onExpand,
}: {
  nodes: TreeNode[];
  active: string;
  onOpen: (path: string) => void;
  onExpand: (node: TreeNode) => void;
}) {
  return (
    <ul className="workspace-tree">
      {nodes.map((node) => (
        <li key={node.path}>
          <button
            className={node.path === active ? "active" : ""}
            onClick={() => (node.is_dir ? onExpand(node) : onOpen(node.path))}
            title={node.path}
          >
            <span aria-hidden="true">
              {node.is_dir ? (node.expanded ? "▾" : "▸") : "·"}
            </span>
            {node.name}
          </button>
          {node.is_dir && node.expanded && node.children ? (
            <FileTree
              nodes={node.children}
              active={active}
              onOpen={onOpen}
              onExpand={onExpand}
            />
          ) : null}
        </li>
      ))}
    </ul>
  );
}

function toOpenFile(file: WorkspaceFile): OpenFile {
  return { ...file, savedContent: file.content };
}

export function WorkspaceShell({
  workDir,
  active,
  children,
  activity,
  browsing,
  chatDir,
  onWorkHere,
  onStopBrowsing,
  showActivity,
  onActivityClosed,
  onDirtyChange,
  onNotify,
  onShowDocks,
  model,
  models,
  layoutPersistence,
}: Props) {
  const [tree, setTree] = useState<TreeNode[]>([]);
  const [files, setFiles] = useState<OpenFile[]>([]);
  const [activePath, setActivePath] = useState("");
  const [git, setGit] = useState<GitStatusResult | null>(null);
  const [gitTabs, setGitTabs] = useState<GitTab[]>([]);
  // A non-empty id means the editor area is showing a diff rather than a file.
  const [activeGitTab, setActiveGitTab] = useState("");
  // Bumped on every index or working-tree change so open diffs re-read.
  const [gitRevision, setGitRevision] = useState(0);
  const [multiRun, setMultiRun] = useState(false);
  const storageKeys = dockStorageKeys(layoutPersistence, workDir);
  const [columns, setColumns] = useState<DockColumns>(() =>
    loadColumns(storageKeys?.layout ?? null),
  );
  // The tool being dragged, and the zone the pointer is over, as
  // `${groupID}:${where}` — only used to light up drop targets.
  const [dragging, setDragging] = useState("");
  const [dropHint, setDropHint] = useState("");
  // The group whose "add panel" menu is open, if any.
  const [addMenu, setAddMenu] = useState("");
  const [menuSlot, setMenuSlot] = useState<HTMLElement | null>(null);
  const [terminalSlot, setTerminalSlot] = useState<HTMLElement | null>(null);
  // Seeded from the restored layout, not from one: a saved Terminal 1 must not
  // be handed the same id twice.
  const nextTerminal = useRef(0);
  const [quickOpen, setQuickOpen] = useState(false);
  const [lightEditorBackground, setLightEditorBackground] = useState(false);
  const [query, setQuery] = useState("");
  const [workspacePaths, setWorkspacePaths] = useState<string[]>([]);
  // One body element per group, tracked in state because the portalled tools
  // can only mount once the element exists. The setters are cached so a group
  // keeps the same ref callback across renders and React does not detach the
  // element it already gave us.
  const [groupBodies, setGroupBodies] = useState<
    Record<string, HTMLDivElement | null>
  >({});
  const groupBodyRefs = useRef(
    new Map<string, (element: HTMLDivElement | null) => void>(),
  );
  const groupBodyRef = (id: string) => {
    const cached = groupBodyRefs.current.get(id);
    if (cached) return cached;
    const assign = (element: HTMLDivElement | null) =>
      setGroupBodies((current) =>
        current[id] === element ? current : { ...current, [id]: element },
      );
    groupBodyRefs.current.set(id, assign);
    return assign;
  };
  const shellRef = useRef<HTMLDivElement>(null);
  const [dockWidths, setDockWidths] = useState<DockWidths>(() =>
    loadDockWidths(storageKeys?.widths ?? null),
  );
  const workspaceGeneration = useRef(0);
  const openRequest = useRef(0);
  const file = files.find((candidate) => candidate.path === activePath) ?? null;
  const dirty = file ? isDirty(file) : false;
  const hasDirtyFiles = files.some(isDirty);

  useEffect(() => onDirtyChange(hasDirtyFiles), [hasDirtyFiles, onDirtyChange]);

  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => {
      if (!hasDirtyFiles) return;
      event.preventDefault();
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [hasDirtyFiles]);

  const applyGit = useCallback((status: GitStatusResult) => {
    setGit(status);
    setGitRevision((n) => n + 1);
  }, []);

  const focusTool = useCallback((id: string) => {
    setColumns((current) => setActiveTool(current, id));
  }, []);

  const refreshGit = useCallback(() => {
    void forge
      .gitStatus()
      .then(applyGit)
      .catch((error: unknown) => onNotify(String(error)));
  }, [applyGit, onNotify]);

  const openGitTab = useCallback(
    (tab: GitTab) => {
      setGitTabs((current) =>
        current.some((open) => open.id === tab.id)
          ? current
          : [...current, tab],
      );
      setActiveGitTab(tab.id);
      // Only one tool per group is visible, so opening a diff has to bring the
      // editor's tab to the front or the click looks like it did nothing.
      focusTool("editor");
    },
    [focusTool],
  );

  const closeGitTab = (id: string) => {
    const index = gitTabs.findIndex((tab) => tab.id === id);
    const remaining = gitTabs.filter((tab) => tab.id !== id);
    setGitTabs(remaining);
    if (activeGitTab === id) {
      setActiveGitTab(
        remaining[Math.min(index, remaining.length - 1)]?.id ?? "",
      );
    }
  };

  const indexWorkspace = useCallback(async (generation: number) => {
    const paths: string[] = [];
    const visit = async (path: string) => {
      const entries = await forge.listWorkspaceDir(path);
      for (const entry of entries) {
        if (generation !== workspaceGeneration.current) return;
        if (entry.is_dir) await visit(entry.path);
        else paths.push(entry.path);
      }
    };
    await visit("");
    if (generation !== workspaceGeneration.current) return;
    paths.sort((a, b) => a.localeCompare(b));
    setWorkspacePaths(paths);
  }, []);

  useEffect(() => {
    const generation = ++workspaceGeneration.current;
    openRequest.current++;
    setFiles([]);
    setActivePath("");
    setGitTabs([]);
    setActiveGitTab("");
    setWorkspacePaths([]);
    void forge
      .listWorkspaceDir("")
      .then((next) => {
        if (generation === workspaceGeneration.current) setTree(next);
      })
      .catch((error: unknown) => onNotify(String(error)));
    void indexWorkspace(generation).catch((error: unknown) =>
      onNotify(String(error)),
    );
    refreshGit();
  }, [workDir, onNotify, refreshGit, indexWorkspace]);

  const expand = (node: TreeNode) => {
    if (node.loaded) {
      setTree((current) =>
        patchTree(current, node.path, { expanded: !node.expanded }),
      );
      return;
    }
    void forge
      .listWorkspaceDir(node.path)
      .then((children) =>
        setTree((current) =>
          patchTree(current, node.path, {
            children,
            expanded: true,
            loaded: true,
          }),
        ),
      )
      .catch((error: unknown) => onNotify(String(error)));
  };

  const openFile = useCallback(
    (path: string) => {
      setActiveGitTab("");
      focusTool("editor");
      if (files.some((candidate) => candidate.path === path)) {
        openRequest.current++;
        setActivePath(path);
        setQuickOpen(false);
        return;
      }
      const request = ++openRequest.current;
      const generation = workspaceGeneration.current;
      void forge
        .readWorkspaceFile(path)
        .then((next) => {
          if (generation !== workspaceGeneration.current) return;
          setFiles((current) =>
            current.some((candidate) => candidate.path === path)
              ? current
              : [...current, toOpenFile(next)],
          );
          if (request === openRequest.current) setActivePath(path);
          setQuickOpen(false);
        })
        .catch((error: unknown) => onNotify(String(error)));
    },
    [files, focusTool, onNotify],
  );

  const closeFile = (path: string) => {
    const closing = files.find((candidate) => candidate.path === path);
    if (
      !closing ||
      (isDirty(closing) &&
        !window.confirm(`Discard unsaved changes to ${path}?`))
    )
      return;
    const index = files.findIndex((candidate) => candidate.path === path);
    const remaining = files.filter((candidate) => candidate.path !== path);
    setFiles(remaining);
    if (activePath === path)
      setActivePath(
        remaining[Math.min(index, remaining.length - 1)]?.path ?? "",
      );
  };

  const save = useCallback(() => {
    if (!file || !isDirty(file)) return;
    const expectedVersion = file.version;
    void forge
      .writeWorkspaceFile(file.path, file.content, file.version)
      .then((saved) => {
        setFiles((current) =>
          current.map((candidate) =>
            candidate.path === saved.path
              ? acceptSavedFile(candidate, saved, expectedVersion)
              : candidate,
          ),
        );
        refreshGit();
        onNotify(`saved ${saved.path}`);
      })
      .catch((error: unknown) => onNotify(String(error)));
  }, [file, onNotify, refreshGit]);

  const updateContent = (content: string) => {
    if (!file) return;
    setFiles((current) =>
      current.map((candidate) =>
        candidate.path === file.path ? { ...candidate, content } : candidate,
      ),
    );
  };

  const revert = () => {
    if (!file) return;
    setFiles((current) =>
      current.map((candidate) =>
        candidate.path === file.path
          ? { ...candidate, content: candidate.savedContent }
          : candidate,
      ),
    );
  };

  useEffect(() => {
    if (!active) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "p") {
        event.preventDefault();
        setQuery("");
        setQuickOpen(true);
      }
      if (event.key === "Escape") setQuickOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [active]);

  // Any click outside the open "add panel" menu dismisses it; the effect is
  // attached after the opening click has finished dispatching.
  useEffect(() => {
    if (!addMenu) return;
    const close = () => setAddMenu("");
    window.addEventListener("click", close);
    return () => window.removeEventListener("click", close);
  }, [addMenu]);

  const matches = useMemo(
    () => filterPaths(workspacePaths, query),
    [workspacePaths, query],
  );

  useEffect(
    () => saveDockWidths(dockWidths, storageKeys?.widths ?? null),
    [dockWidths, storageKeys?.widths],
  );

  useEffect(() => {
    setMenuSlot(document.getElementById("forge-panel-menu"));
    setTerminalSlot(document.getElementById("forge-terminal-button"));
  }, []);

  useEffect(
    () => saveColumns(columns, storageKeys?.layout ?? null),
    [columns, storageKeys?.layout],
  );

  const resizeDock = useCallback((side: EdgeSide, fraction: number) => {
    setDockWidths((current) => clampDock(current, side, fraction));
  }, []);

  const startDockDrag = (side: EdgeSide) => (event: React.PointerEvent) => {
    event.preventDefault();
    const move = (pointer: PointerEvent) => {
      const rect = shellRef.current?.getBoundingClientRect();
      if (rect) resizeDock(side, dockFraction(side, pointer.clientX, rect));
    };
    const stop = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", stop);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", stop);
  };

  // Arrow keys move a focused divider, so the docks are resizable without a
  // pointer; double-click restores the default width.
  const dockKeyDown = (side: EdgeSide) => (event: React.KeyboardEvent) => {
    const step =
      event.key === "ArrowLeft" ? -0.02 : event.key === "ArrowRight" ? 0.02 : 0;
    if (!step) return;
    event.preventDefault();
    const direction = side === "left" ? step : -step;
    resizeDock(side, dockWidths[side] + direction);
  };

  const dropTool = (id: string, target: DropTarget) => {
    setColumns((current) => moveTool(current, id, target));
    setDragging("");
    setDropHint("");
  };

  // The keyboard route to the same thing dragging does: send a panel round the
  // columns, left to chat to right.
  const cycleTool = (tool: DockTool, side: DockSide) => {
    const next = SIDES[(SIDES.indexOf(side) + 1) % SIDES.length];
    dropTool(tool.id, { side: next, where: "end" });
  };

  const rightTabTarget = (current: DockColumns): DropTarget => {
    const group = current.right[0];
    return group
      ? { side: "right", where: "into", groupID: group.id }
      : { side: "right", where: "end" };
  };

  const launchTerminal = () => {
    setColumns((current) => {
      // Taken from the layout being added to rather than from a counter, so a
      // terminal restored from storage or opened in another window cannot be
      // shadowed by a new one with its id.
      const number = Math.max(
        nextTerminal.current,
        nextTerminalNumber(current),
      );
      nextTerminal.current = number + 1;
      const tool: DockTool = {
        id: `terminal-${number}`,
        kind: "terminal",
        title: `Terminal ${number}`,
      };
      return setActiveTool(
        addTool(current, tool, rightTabTarget(current)),
        tool.id,
      );
    });
  };

  // The preview is a single pane — a second one would just be the same app
  // twice — so asking for it again brings the existing one forward.
  const openPreview = () => {
    setColumns((current) =>
      findTool(current, "preview")
        ? setActiveTool(current, "preview")
        : setActiveTool(
            addTool(
              current,
              { id: "preview", kind: "preview", title: "Preview" },
              rightTabTarget(current),
            ),
            "preview",
          ),
    );
  };

  // The progress panel is a single pane like the preview: asking for it again
  // brings the existing one forward instead of stacking a copy.
  const openActivity = () => {
    setColumns((current) =>
      findTool(current, ACTIVITY_TOOL.id)
        ? setActiveTool(current, ACTIVITY_TOOL.id)
        : setActiveTool(
            addTool(current, ACTIVITY_TOOL, rightTabTarget(current)),
            ACTIVITY_TOOL.id,
          ),
    );
  };

  // The setting and the dock are kept in step: turning it on docks the panel,
  // turning it off removes it.
  useEffect(() => {
    setColumns((current) => {
      const docked = Boolean(findTool(current, ACTIVITY_TOOL.id));
      if (showActivity === docked) return current;
      return showActivity
        ? setActiveTool(
            addTool(current, ACTIVITY_TOOL, rightTabTarget(current)),
            ACTIVITY_TOOL.id,
          )
        : removeTool(current, ACTIVITY_TOOL.id);
    });
  }, [showActivity]);

  const closePanel = (id: string) => {
    if (id === ACTIVITY_TOOL.id) onActivityClosed();
    setColumns((current) => {
      const kind = findTool(current, id)?.tool.kind;
      return kind === "terminal" || kind === "preview" || kind === "activity"
        ? removeTool(current, id)
        : current;
    });
  };

  // A group divider splits the space its two neighbours share; both are
  // measured when the drag starts so the pointer keeps tracking the divider
  // even as the groups resize under it.
  const startGroupDrag =
    (side: DockSide, index: number) => (event: React.PointerEvent) => {
      event.preventDefault();
      const divider = event.currentTarget as HTMLElement;
      const above = divider.previousElementSibling?.getBoundingClientRect();
      const below = divider.nextElementSibling?.getBoundingClientRect();
      if (!above || !below) return;
      const top = above.top;
      const height = below.bottom - above.top;
      const move = (pointer: PointerEvent) => {
        setColumns((current) =>
          resizeGroups(current, side, index, (pointer.clientY - top) / height),
        );
      };
      const stop = () => {
        window.removeEventListener("pointermove", move);
        window.removeEventListener("pointerup", stop);
      };
      window.addEventListener("pointermove", move);
      window.addEventListener("pointerup", stop);
    };

  const groupKeyDown =
    (side: DockSide, index: number) => (event: React.KeyboardEvent) => {
      const step =
        event.key === "ArrowUp" ? -0.05 : event.key === "ArrowDown" ? 0.05 : 0;
      if (!step) return;
      event.preventDefault();
      setColumns((current) => {
        const groups = current[side];
        const pair = groups[index - 1].size + groups[index].size;
        return resizeGroups(
          current,
          side,
          index,
          groups[index - 1].size / pair + step,
        );
      });
    };

  const tabDropProps = (side: DockSide, group: DockGroup, index: number) => {
    const hintFor = (at: number) => `${group.id}:tab:${at}`;
    return {
      onDragOver: (event: React.DragEvent) => {
        if (!dragging && !event.dataTransfer.types.includes(DOCK_TOOL_MIME))
          return;
        event.preventDefault();
        event.dataTransfer.dropEffect = "move" as const;
        const box = event.currentTarget.getBoundingClientRect();
        const after = event.clientX > box.left + box.width / 2;
        setDropHint(hintFor(after ? index + 1 : index));
      },
      onDrop: (event: React.DragEvent) => {
        const id = event.dataTransfer.getData(DOCK_TOOL_MIME) || dragging;
        if (!id) return;
        event.preventDefault();
        event.stopPropagation();
        const box = event.currentTarget.getBoundingClientRect();
        const at = event.clientX > box.left + box.width / 2 ? index + 1 : index;
        dropTool(id, { side, where: "into", groupID: group.id, index: at });
      },
      className: [
        dropHint === hintFor(index) ? "drop-before" : "",
        dropHint === hintFor(index + 1) ? "drop-after" : "",
      ]
        .filter(Boolean)
        .join(" "),
    };
  };

  const dropProps = (target: DropTarget) => {
    const hint =
      target.where === "end"
        ? `${target.side}:end`
        : `${target.groupID}:${target.where}`;
    return {
      onDragOver: (event: React.DragEvent) => {
        if (!dragging && !event.dataTransfer.types.includes(DOCK_TOOL_MIME))
          return;
        event.preventDefault();
        event.dataTransfer.dropEffect = "move" as const;
        setDropHint(hint);
      },
      onDragLeave: () =>
        setDropHint((current) => (current === hint ? "" : current)),
      onDrop: (event: React.DragEvent) => {
        const id = event.dataTransfer.getData(DOCK_TOOL_MIME) || dragging;
        if (!id) return;
        event.preventDefault();
        dropTool(id, target);
      },
      className: dropHint === hint ? "over" : "",
    };
  };

  const renderTool = (tool: DockTool, layoutKey = "") => {
    if (tool.kind === "chat") return children;
    if (tool.kind === "activity") return activity;
    if (tool.kind === "explorer")
      return (
        <>
          <div className="workspace-root" title={workDir}>
            {workDir.split("/").pop() || workDir}
          </div>
          <FileTree
            nodes={tree}
            active={activePath}
            onOpen={openFile}
            onExpand={expand}
          />
        </>
      );
    if (tool.kind === "git")
      return (
        <GitPanel
          status={git}
          onStatus={applyGit}
          onRefresh={refreshGit}
          onOpenTab={openGitTab}
          onOpenFile={openFile}
          onNotify={onNotify}
          onMultiRun={() => setMultiRun(true)}
          model={model}
        />
      );
    if (tool.kind === "preview")
      return <PreviewPanel workDir={workDir} onNotify={onNotify} />;
    if (tool.kind === "terminal")
      return (
        <TerminalWorkspace
          workDir={workDir}
          instanceID={tool.id}
          layoutKey={layoutKey}
          onNotify={onNotify}
        />
      );
    const diffTab = gitTabs.find((tab) => tab.id === activeGitTab) ?? null;
    return (
      <section className="workspace-editor">
        {files.length > 0 || gitTabs.length > 0 ? (
          <div className="workspace-tabs">
            {files.map((open) => (
              <div
                className={`workspace-tab ${!diffTab && open.path === activePath ? "active" : ""}`}
                key={open.path}
                title={open.path}
              >
                <button
                  onClick={() => {
                    setActiveGitTab("");
                    setActivePath(open.path);
                  }}
                >
                  {open.path.split("/").pop()}
                  {isDirty(open) ? " ●" : ""}
                </button>
                <button
                  className="workspace-tab-close"
                  onClick={() => closeFile(open.path)}
                  aria-label={`Close ${open.path}`}
                >
                  ×
                </button>
              </div>
            ))}
            {gitTabs.map((tab) => (
              <div
                className={`workspace-tab diff ${tab.id === activeGitTab ? "active" : ""}`}
                key={tab.id}
                title={tab.id}
              >
                <button onClick={() => setActiveGitTab(tab.id)}>
                  <span aria-hidden="true">±</span> {tab.title}
                </button>
                <button
                  className="workspace-tab-close"
                  onClick={() => closeGitTab(tab.id)}
                  aria-label={`Close ${tab.title}`}
                >
                  ×
                </button>
              </div>
            ))}
          </div>
        ) : null}
        <div className="workspace-toolbar">
          <span className="workspace-file-name">
            {diffTab ? diffTab.title : file ? file.path : "No file open"}
          </span>
          <button
            className="workspace-quick-open-button"
            onClick={() => {
              setQuery("");
              setQuickOpen(true);
            }}
            title="Quick open (Ctrl/Command+P)"
            aria-label="Quick open file"
          >
            ⌕
          </button>
          <button
            aria-label={`Use ${lightEditorBackground ? "dark" : "light"} editor background`}
            aria-pressed={lightEditorBackground}
            className="workspace-editor-theme-button"
            onClick={() => setLightEditorBackground((light) => !light)}
            title={`Use ${lightEditorBackground ? "dark" : "light"} editor background`}
          >
            <span aria-hidden="true">{lightEditorBackground ? "☾" : "☀"}</span>
          </button>
          <button disabled={!dirty} onClick={revert}>
            Revert
          </button>
          <button disabled={!dirty} onClick={save}>
            Save
          </button>
        </div>
        {diffTab ? (
          <GitTabView
            key={diffTab.id}
            tab={diffTab}
            model={model}
            revision={gitRevision}
            onOpenFile={openFile}
            onNotify={onNotify}
            onStage={(path) => {
              void forge
                .gitStage([path])
                .then(applyGit)
                .catch((error: unknown) => onNotify(String(error)));
            }}
            onUnstage={(path) => {
              void forge
                .gitUnstage([path])
                .then(applyGit)
                .catch((error: unknown) => onNotify(String(error)));
            }}
          />
        ) : file ? (
          <CodeEditor
            key={file.path}
            path={file.path}
            value={file.content}
            lightBackground={lightEditorBackground}
            onChange={updateContent}
            onSave={save}
            onNotify={onNotify}
          />
        ) : (
          <div className="workspace-empty">Select a text file to edit</div>
        )}
      </section>
    );
  };

  // New panels land at the foot of the right-hand column: it is the widest
  // dock by default and always on screen, and from there they can be dragged
  // anywhere.
  const openPanel = (open: () => void) => {
    setAddMenu("");
    onShowDocks();
    open();
  };

  // The title bar's slot only exists once the header has mounted, so it is
  // looked up after the first paint rather than during render.
  const terminalButton = terminalSlot
    ? createPortal(
        <button
          aria-label="New Terminal"
          className="icon-btn"
          onClick={() => openPanel(launchTerminal)}
          title="New Terminal"
        >
          <svg
            aria-hidden="true"
            fill="none"
            height="16"
            viewBox="0 0 16 16"
            width="16"
          >
            <rect
              height="12"
              rx="1.5"
              stroke="currentColor"
              width="14"
              x="1"
              y="2"
            />
            <path
              d="m4 5 2.5 2.5L4 10m4 0h3.5"
              stroke="currentColor"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </button>,
        terminalSlot,
      )
    : null;
  const panelMenu = menuSlot;
  const panelsMenu = panelMenu
    ? createPortal(
        <span className="topbar-menu">
          <button
            aria-expanded={addMenu === "panels"}
            aria-haspopup="menu"
            className="pill"
            onClick={(event) => {
              event.stopPropagation();
              setAddMenu((current) => (current === "panels" ? "" : "panels"));
            }}
            title="Open a panel"
          >
            Panels ▾
          </button>
          {addMenu === "panels" ? (
            <div className="topbar-dropdown" role="menu">
              <button onClick={() => openPanel(launchTerminal)} role="menuitem">
                New Terminal
              </button>
              <button onClick={() => openPanel(openPreview)} role="menuitem">
                Preview
              </button>
              <button onClick={() => openPanel(openActivity)} role="menuitem">
                Progress
              </button>
              <hr />
              {[
                { id: "explorer", label: "Explorer" },
                { id: "git", label: "Source Control" },
                { id: "editor", label: "Editor" },
              ].map((entry) => (
                <button
                  key={entry.id}
                  onClick={() => openPanel(() => focusTool(entry.id))}
                  role="menuitem"
                >
                  {entry.label}
                </button>
              ))}
            </div>
          ) : null}
        </span>,
        panelMenu,
      )
    : null;

  // One unmissable line, rather than a subtle cue: which repository the panels
  // are showing, which one the agent is in, and both ways out.
  const browseBanner = browsing ? (
    <div className="browse-banner">
      <span className="browse-what">
        Browsing <b>{browsing.split("/").pop()}</b> — read-only
      </span>
      <span className="browse-where">
        the agent is in <b>{chatDir.split("/").pop()}</b>
      </span>
      <button className="btn" onClick={() => onWorkHere(browsing)}>
        Work here
      </button>
      <button className="btn" onClick={onStopBrowsing}>
        Back to {chatDir.split("/").pop()}
      </button>
    </div>
  ) : null;

  const renderGroup = (side: DockSide, group: DockGroup) => {
    // A tab strip is how you pick between tools and where you drop one to
    // stack it. A group holding nothing but the chat has neither job to do, so
    // it goes without: the chat is the window, not a tab in it.
    const soloChat = group.tools.length === 1 && group.tools[0].kind === "chat";
    return (
      <section
        className={`workspace-group ${group.tools.some((tool) => tool.kind === "chat") ? "has-chat" : ""} ${soloChat ? "chat-only" : ""}`}
        style={{ flexGrow: group.size }}
      >
        {soloChat ? null : (
          <div
            {...dropProps({ side, where: "into", groupID: group.id })}
            className={`workspace-dock-tabs ${dropProps({ side, where: "into", groupID: group.id }).className}`}
          >
            {group.tools.map((tool, index) => {
              const tabDrop = tabDropProps(side, group, index);
              return (
                <div
                  aria-selected={group.activeID === tool.id}
                  className={`workspace-dock-tab ${group.activeID === tool.id ? "active" : ""} ${tabDrop.className}`}
                  draggable
                  key={tool.id}
                  onClick={() => focusTool(tool.id)}
                  onDragEnd={() => {
                    setDragging("");
                    setDropHint("");
                  }}
                  onDragOver={tabDrop.onDragOver}
                  onDragStart={(event) => {
                    event.dataTransfer.effectAllowed = "move";
                    event.dataTransfer.setData(DOCK_TOOL_MIME, tool.id);
                    event.dataTransfer.setData("text/plain", tool.id);
                    // Let the browser establish the native drag before opening
                    // empty columns and mounting drop zones. Changing the centre
                    // layout synchronously during dragstart can cancel the drag
                    // in the WebKit host, leaving panels moved into chat stuck.
                    requestAnimationFrame(() => setDragging(tool.id));
                  }}
                  onDrop={tabDrop.onDrop}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ") {
                      event.preventDefault();
                      focusTool(tool.id);
                    }
                  }}
                  role="tab"
                  tabIndex={0}
                  title={`${tool.title} — drag onto another panel to move or split it`}
                >
                  <span className="workspace-dock-tab-label">{tool.title}</span>
                  {tool.kind === "terminal" ||
                  tool.kind === "preview" ||
                  tool.kind === "activity" ? (
                    <button
                      className="workspace-tab-close"
                      onClick={(event) => {
                        event.stopPropagation();
                        closePanel(tool.id);
                      }}
                      aria-label={`Close ${tool.title}`}
                    >
                      ×
                    </button>
                  ) : null}
                </div>
              );
            })}
          </div>
        )}
        <div className="workspace-group-body" ref={groupBodyRef(group.id)} />
        {dragging ? (
          <div className="workspace-dropzones">
            {(["before", "into", "after"] as const).map((where) => (
              <div
                key={where}
                {...dropProps({ side, where, groupID: group.id })}
                className={`workspace-dropzone workspace-dropzone-${where} ${dropProps({ side, where, groupID: group.id }).className}`}
              />
            ))}
          </div>
        ) : null}
      </section>
    );
  };

  return (
    <div className="workspace-frame">
      {browseBanner}
      <div
        className={`workspace-shell ${active ? "docks-open" : "docks-closed"} ${browsing ? "browsing" : ""}`}
        ref={shellRef}
        style={
          {
            "--dock-left": `${dockWidths.left * 100}%`,
            "--dock-right": `${dockWidths.right * 100}%`,
          } as React.CSSProperties
        }
      >
        {SIDES.map((side, columnIndex) => {
          // The divider before a column resizes the dock on its left: the one
          // before the chat sizes the left dock, the one after it the right.
          const edge: EdgeSide = side === "center" ? "left" : "right";
          const tail = dropProps({ side, where: "end" });
          const empty = columns[side].length === 0;
          const { collapsed, basis, grow } = columnLayout(
            columns,
            side,
            dockWidths,
            Boolean(dragging),
          );
          const showDivider = showsDivider(columns, side, Boolean(dragging));
          return (
            <Fragment key={side}>
              {showDivider ? (
                <div
                  aria-label={`Resize ${edge} panel`}
                  aria-orientation="vertical"
                  aria-valuenow={Math.round(dockWidths[edge] * 100)}
                  className={`workspace-divider workspace-divider-${edge}`}
                  onDoubleClick={() =>
                    resizeDock(edge, DEFAULT_DOCK_WIDTHS[edge])
                  }
                  onKeyDown={dockKeyDown(edge)}
                  onPointerDown={startDockDrag(edge)}
                  role="separator"
                  tabIndex={0}
                />
              ) : null}
              <div
                className={`workspace-column workspace-column-${side} ${collapsed ? "collapsed" : ""}`}
                style={{
                  ...(basis === undefined ? {} : { flexBasis: basis }),
                  ...(grow === undefined ? {} : { flexGrow: grow }),
                }}
              >
                {columns[side].map((group, index) => (
                  <Fragment key={group.id}>
                    {index > 0 ? (
                      <div
                        aria-label={`Resize ${side} panels`}
                        aria-orientation="horizontal"
                        className="workspace-group-divider"
                        onKeyDown={groupKeyDown(side, index)}
                        onPointerDown={startGroupDrag(side, index)}
                        role="separator"
                        tabIndex={0}
                      />
                    ) : null}
                    {renderGroup(side, group)}
                  </Fragment>
                ))}
                <div
                  {...tail}
                  className={`workspace-column-tail ${empty ? "empty" : ""} ${tail.className}`}
                >
                  {empty && dragging ? "Drop a panel here" : null}
                </div>
              </div>
            </Fragment>
          );
        })}
        {allTools(columns).map((tool) => {
          const home = findTool(columns, tool.id);
          if (!home) return null;
          return (
            <DockToolHost
              active={
                // With the docks closed only the chat is on screen, whichever tab
                // its group last had selected.
                active ? home.group.activeID === tool.id : tool.kind === "chat"
              }
              key={tool.id}
              kind={tool.kind}
              target={groupBodies[home.group.id] ?? null}
            >
              {tool.kind === "chat" ? null : (
                <div className="workspace-tool-actions">
                  <button
                    onClick={() => cycleTool(tool, home.side)}
                    title="Move this panel to the next column"
                    aria-label={`Move ${tool.title} to the next column`}
                  >
                    ⇄
                  </button>
                </div>
              )}
              {renderTool(
                tool,
                `${home.side}:${home.group.id}:${home.group.activeID}`,
              )}
            </DockToolHost>
          );
        })}
        {panelsMenu}
        {terminalButton}
        {quickOpen ? (
          <div
            className="workspace-quick-open"
            role="dialog"
            aria-label="Quick open file"
            onMouseDown={(event) => {
              if (event.target === event.currentTarget) setQuickOpen(false);
            }}
          >
            <div className="workspace-quick-open-panel">
              <input
                autoFocus
                aria-label="File name"
                placeholder="Search files by name"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" && matches[0]) openFile(matches[0]);
                }}
              />
              <ul>
                {matches.map((path, index) => (
                  <li key={path}>
                    <button
                      className={index === 0 ? "selected" : ""}
                      onClick={() => openFile(path)}
                    >
                      {path}
                    </button>
                  </li>
                ))}
              </ul>
              {matches.length === 0 ? (
                <div className="workspace-muted">No matching files</div>
              ) : null}
            </div>
          </div>
        ) : null}
        {multiRun ? (
          <MultiRunDialog
            models={models}
            currentModel={model}
            isRepo={git?.repository ?? false}
            onClose={() => setMultiRun(false)}
            onNotify={onNotify}
          />
        ) : null}
      </div>
    </div>
  );
}
