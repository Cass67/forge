import {
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
  clampDock,
  DEFAULT_DOCK_WIDTHS,
  dockFraction,
  type DockWidths,
  loadDockWidths,
  saveDockWidths,
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
type DockSide = "left" | "right";
type ToolKind = "explorer" | "editor" | "git" | "terminal";
type DockTool = { id: string; kind: ToolKind; title: string; side: DockSide };
const DOCK_TOOL_MIME = "application/x-forge-dock-tool";

function DockToolHost({
  target,
  active,
  side,
  children,
}: {
  target: HTMLDivElement | null;
  active: boolean;
  side: DockSide;
  children: ReactNode;
}) {
  const [host] = useState(() => document.createElement("div"));

  useLayoutEffect(() => {
    host.className = `workspace-tool workspace-tool-${side} ${active ? "active" : ""}`;
    target?.appendChild(host);
  }, [active, host, side, target]);

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
  const [tools, setTools] = useState<DockTool[]>([
    { id: "explorer", kind: "explorer", title: "Explorer", side: "left" },
    { id: "editor", kind: "editor", title: "Editor", side: "right" },
    // Source control shares the left dock with the explorer on purpose: it
    // opens diffs into the editor, so parking it in the editor's own dock
    // would hide the pane it is driving.
    { id: "git", kind: "git", title: "Source Control", side: "left" },
  ]);
  const [activeTool, setActiveTool] = useState<Record<DockSide, string>>({
    left: "explorer",
    right: "editor",
  });
  const [dropSide, setDropSide] = useState<DockSide | null>(null);
  const nextTerminal = useRef(1);
  const [quickOpen, setQuickOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [workspacePaths, setWorkspacePaths] = useState<string[]>([]);
  const [dockBodies, setDockBodies] = useState<
    Record<DockSide, HTMLDivElement | null>
  >({ left: null, right: null });
  const dockBodyRefs = useMemo(() => {
    const assign = (side: DockSide) => (element: HTMLDivElement | null) =>
      setDockBodies((current) =>
        current[side] === element ? current : { ...current, [side]: element },
      );
    return { left: assign("left"), right: assign("right") };
  }, []);
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
      // Only one tool per dock is visible, so opening a diff has to bring the
      // editor's dock to the front or the click looks like it did nothing.
      const editor = tools.find((tool) => tool.kind === "editor");
      if (editor) {
        setActiveTool((current) =>
          current[editor.side] === editor.id
            ? current
            : { ...current, [editor.side]: editor.id },
        );
      }
    },
    [tools],
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
      const editor = tools.find((tool) => tool.kind === "editor");
      if (editor) {
        setActiveTool((current) =>
          current[editor.side] === editor.id
            ? current
            : { ...current, [editor.side]: editor.id },
        );
      }
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
    [files, onNotify, tools],
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

  const matches = useMemo(
    () => filterPaths(workspacePaths, query),
    [workspacePaths, query],
  );

  useEffect(() => saveDockWidths(dockWidths), [dockWidths]);

  const resizeDock = useCallback((side: DockSide, fraction: number) => {
    setDockWidths((current) => clampDock(current, side, fraction));
  }, []);

  const startDockDrag = (side: DockSide) => (event: React.PointerEvent) => {
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
  const dockKeyDown = (side: DockSide) => (event: React.KeyboardEvent) => {
    const step =
      event.key === "ArrowLeft" ? -0.02 : event.key === "ArrowRight" ? 0.02 : 0;
    if (!step) return;
    event.preventDefault();
    const direction = side === "left" ? step : -step;
    resizeDock(side, dockWidths[side] + direction);
  };

  const moveTool = (id: string, side: DockSide) => {
    setTools((current) => {
      const moving = current.find((tool) => tool.id === id);
      if (!moving || moving.side === side) return current;
      const next = current.map((tool) =>
        tool.id === id ? { ...tool, side } : tool,
      );
      setActiveTool((activeTools) => ({
        ...activeTools,
        [moving.side]:
          activeTools[moving.side] === id
            ? (next.find((tool) => tool.side === moving.side)?.id ?? "")
            : activeTools[moving.side],
        [side]: id,
      }));
      return next;
    });
  };

  const launchTerminal = (side: DockSide) => {
    const number = nextTerminal.current++;
    const id = `terminal-${number}`;
    setTools((current) => [
      ...current,
      { id, kind: "terminal", title: `Terminal ${number}`, side },
    ]);
    setActiveTool((current) => ({ ...current, [side]: id }));
  };

  const closeTerminal = (id: string) => {
    const closing = tools.find((tool) => tool.id === id);
    if (!closing || closing.kind !== "terminal") return;
    const remaining = tools.filter((tool) => tool.id !== id);
    setTools(remaining);
    if (activeTool[closing.side] === id) {
      setActiveTool((current) => ({
        ...current,
        [closing.side]:
          remaining.find((tool) => tool.side === closing.side)?.id ?? "",
      }));
    }
  };

  const renderTool = (tool: DockTool) => {
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
      {(["left", "right"] as const).map((side) => (
        <aside
          className={`workspace-dock workspace-dock-${side} ${dropSide === side ? "drag-over" : ""}`}
          key={side}
          onDragOver={(event) => {
            if (!event.dataTransfer.types.includes(DOCK_TOOL_MIME)) return;
            event.preventDefault();
            event.dataTransfer.dropEffect = "move";
            setDropSide(side);
          }}
          onDrop={(event) => {
            event.preventDefault();
            moveTool(event.dataTransfer.getData(DOCK_TOOL_MIME), side);
            setDropSide(null);
          }}
        >
          <div className="workspace-dock-tabs">
            {tools
              .filter((tool) => tool.side === side)
              .map((tool) => (
                <button
                  className={activeTool[side] === tool.id ? "active" : ""}
                  draggable
                  key={tool.id}
                  onDragEnd={() => setDropSide(null)}
                  onDragStart={(event) => {
                    event.dataTransfer.effectAllowed = "move";
                    event.dataTransfer.setData(DOCK_TOOL_MIME, tool.id);
                  }}
                  onClick={() =>
                    setActiveTool((current) => ({
                      ...current,
                      [side]: tool.id,
                    }))
                  }
                >
                  {tool.title}
                  {tool.kind === "terminal" ? (
                    <span
                      onClick={(event) => {
                        event.stopPropagation();
                        closeTerminal(tool.id);
                      }}
                    >
                      {" "}
                      ×
                    </span>
                  ) : null}
                </button>
              ))}
            <button
              onClick={() => launchTerminal(side)}
              title={`New terminal in ${side} dock`}
            >
              ＋
            </button>
          </div>
        </aside>
      ))}
      {(["left", "right"] as const).map((side) => (
        <div
          aria-label={`Resize ${side} panel`}
          aria-orientation="vertical"
          aria-valuenow={Math.round(dockWidths[side] * 100)}
          className={`workspace-divider workspace-divider-${side}`}
          key={`${side}-divider`}
          onDoubleClick={() => resizeDock(side, DEFAULT_DOCK_WIDTHS[side])}
          onKeyDown={dockKeyDown(side)}
          onPointerDown={startDockDrag(side)}
          role="separator"
          tabIndex={0}
        />
      ))}
      <div className="workspace-chat">{children}</div>
      {(["left", "right"] as const).map((side) => (
        <div
          className={`workspace-dock-body workspace-dock-body-${side}`}
          key={`${side}-body`}
          ref={dockBodyRefs[side]}
        />
      ))}
      {tools.map((tool) => (
        <DockToolHost
          active={activeTool[tool.side] === tool.id}
          key={tool.id}
          side={tool.side}
          target={dockBodies[tool.side]}
        >
          <div className="workspace-tool-actions">
            <button
              onClick={() =>
                moveTool(tool.id, tool.side === "left" ? "right" : "left")
              }
              title={`Move to ${tool.side === "left" ? "right" : "left"} dock`}
            >
              {tool.side === "left" ? "→" : "←"}
            </button>
          </div>
          {renderTool(tool)}
        </DockToolHost>
      ))}
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
