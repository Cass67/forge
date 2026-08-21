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
  addTool,
  allTools,
  clampDock,
  DEFAULT_DOCK_WIDTHS,
  type DockColumns,
  type DockGroup,
  type DockSide,
  type DockTool,
  dockFraction,
  type DockWidths,
  type DropTarget,
  dropZone,
  findTool,
  loadColumns,
  loadDockWidths,
  moveTool,
  removeTool,
  resizeGroups,
  saveColumns,
  saveDockWidths,
  setActiveTool,
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
  onDirtyChange: (dirty: boolean) => void;
  onNotify: (message: string) => void;
  // The chat's current model and the models it can switch to: the source
  // control panel drafts commit messages with the former and multi-run
  // launches windows across the latter.
  model: string;
  models: string[];
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
  return createPortal(children, host);
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
  onDirtyChange,
  onNotify,
  model,
  models,
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
  // The panel layout: three columns of stacked groups, restored from the last
  // session so a dragged panel is still where the user left it.
  const [columns, setColumns] = useState<DockColumns>(loadColumns);
  // The tool being dragged, and the zone the pointer is over, as
  // `${groupID}:${where}` — only used to light up drop targets.
  const [dragging, setDragging] = useState("");
  const [dropHint, setDropHint] = useState("");
  // The group whose "add panel" menu is open, if any.
  const [addMenu, setAddMenu] = useState("");
  const nextTerminal = useRef(1);
  const [quickOpen, setQuickOpen] = useState(false);
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
  const [dockWidths, setDockWidths] = useState<DockWidths>(loadDockWidths);
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

  useEffect(() => saveDockWidths(dockWidths), [dockWidths]);

  useEffect(() => saveColumns(columns), [columns]);

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

  const launchTerminal = (target: DropTarget) => {
    const number = nextTerminal.current++;
    const tool: DockTool = {
      id: `terminal-${number}`,
      kind: "terminal",
      title: `Terminal ${number}`,
    };
    setColumns((current) =>
      setActiveTool(addTool(current, tool, target), tool.id),
    );
  };

  // The preview is a single pane — a second one would just be the same app
  // twice — so asking for it again brings the existing one forward.
  const openPreview = (target: DropTarget) => {
    setColumns((current) =>
      findTool(current, "preview")
        ? setActiveTool(current, "preview")
        : setActiveTool(
            addTool(
              current,
              { id: "preview", kind: "preview", title: "Preview" },
              target,
            ),
            "preview",
          ),
    );
  };

  const closePanel = (id: string) => {
    setColumns((current) => {
      const kind = findTool(current, id)?.tool.kind;
      return kind === "terminal" || kind === "preview"
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

  const dropProps = (target: DropTarget) => {
    const hint =
      target.where === "end"
        ? `${target.side}:end`
        : `${target.groupID}:${target.where}`;
    return {
      onDragOver: (event: React.DragEvent) => {
        if (!event.dataTransfer.types.includes(DOCK_TOOL_MIME)) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = "move" as const;
        setDropHint(hint);
      },
      onDragLeave: () =>
        setDropHint((current) => (current === hint ? "" : current)),
      onDrop: (event: React.DragEvent) => {
        const id = event.dataTransfer.getData(DOCK_TOOL_MIME);
        if (!id) return;
        event.preventDefault();
        dropTool(id, target);
      },
      className: dropHint === hint ? "over" : "",
    };
  };

  const renderTool = (tool: DockTool) => {
    if (tool.kind === "chat") return children;
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

  const renderGroup = (side: DockSide, group: DockGroup) => (
    <section
      className={`workspace-group ${group.tools.some((tool) => tool.kind === "chat") ? "has-chat" : ""}`}
      style={{ flexGrow: group.size }}
    >
      <div
        {...dropProps({ side, where: "into", groupID: group.id })}
        className={`workspace-dock-tabs ${dropProps({ side, where: "into", groupID: group.id }).className}`}
      >
        {group.tools.map((tool) => (
          <div
            className={`workspace-dock-tab ${group.activeID === tool.id ? "active" : ""}`}
            key={tool.id}
          >
            <button
              draggable
              onClick={() => focusTool(tool.id)}
              onDragEnd={() => {
                setDragging("");
                setDropHint("");
              }}
              onDragStart={(event) => {
                event.dataTransfer.effectAllowed = "move";
                event.dataTransfer.setData(DOCK_TOOL_MIME, tool.id);
                setDragging(tool.id);
              }}
              title={`${tool.title} — drag onto another panel to move or split it`}
            >
              {tool.title}
            </button>
            {tool.kind === "terminal" || tool.kind === "preview" ? (
              <button
                className="workspace-tab-close"
                onClick={() => closePanel(tool.id)}
                aria-label={`Close ${tool.title}`}
              >
                ×
              </button>
            ) : null}
          </div>
        ))}
        <div className="workspace-dock-add">
          <button
            aria-expanded={addMenu === group.id}
            aria-haspopup="menu"
            onClick={() =>
              setAddMenu((current) => (current === group.id ? "" : group.id))
            }
            title="Add a panel here"
          >
            ＋ Panel
          </button>
          {addMenu === group.id ? (
            <div className="workspace-add-menu" role="menu">
              <button
                onClick={() => {
                  setAddMenu("");
                  launchTerminal({ side, where: "into", groupID: group.id });
                }}
                role="menuitem"
              >
                Terminal
              </button>
              <button
                onClick={() => {
                  setAddMenu("");
                  openPreview({ side, where: "into", groupID: group.id });
                }}
                role="menuitem"
              >
                Preview
              </button>
            </div>
          ) : null}
        </div>
      </div>
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

  return (
    <div
      className={`workspace-shell ${active ? "docks-open" : "docks-closed"}`}
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
        return (
          <Fragment key={side}>
            {columnIndex > 0 ? (
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
              className={`workspace-column workspace-column-${side}`}
              style={
                side === "center"
                  ? undefined
                  : { flexBasis: `${dockWidths[side] * 100}%` }
              }
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
                className={`workspace-column-tail ${columns[side].length === 0 ? "empty" : ""} ${tail.className}`}
              >
                {columns[side].length === 0 ? "Drop a panel here" : null}
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
            {renderTool(tool)}
          </DockToolHost>
        );
      })}
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
  );
}
