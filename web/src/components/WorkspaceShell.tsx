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
  loadRuntimeColumns,
  loadDockWidths,
  moveTool,
  nextTerminalNumber,
  removeTool,
  resizeGroups,
  saveRuntimeColumns,
  saveDockWidths,
  setActiveTool,
  showsDivider,
  SIDES,
} from "../dockLayout";
import {
  acceptSavedFile,
  DEFAULT_SCRATCH_EXPANDED,
  filterPaths,
  isDirty,
  type OpenFile,
} from "../workspaceFiles";
import type { Theme } from "../theme";
import type { GitTab } from "../gitTabs";
import { CodeEditor } from "./CodeEditor";
import { ConfirmDialog, NameDialog } from "./FileDialog";
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
  onTerminalPresenceChange: (
    workspace: string,
    id: string,
    open: boolean,
  ) => void;
  onTerminalPanelClose: (workspace: string, panelID: string) => void;
  // Opening a panel from the menu while the docks are hidden has to show them
  // again, or the click looks like it did nothing.
  onShowDocks: () => void;
  // The chat's current model and the models it can switch to: the source
  // control panel drafts commit messages with the former and multi-run
  // launches windows across the latter.
  model: string;
  models: string[];
  layoutPersistence: DockLayoutPersistence;
  theme: Theme;
  vividness: number;
  scratchDir: string;
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

// collapseTree clears the expanded flag on every directory so the explorer
// returns to its flat, top-level view.
function collapseTree(nodes: TreeNode[]): TreeNode[] {
  return nodes.map((node) => {
    if (!node.is_dir) return node;
    return {
      ...node,
      expanded: false,
      children: node.children ? collapseTree(node.children) : node.children,
    };
  });
}

function FileTree({
  nodes,
  active,
  onOpen,
  onExpand,
  onContext,
}: {
  nodes: TreeNode[];
  active: string;
  onOpen: (path: string) => void;
  onExpand: (node: TreeNode) => void;
  onContext: (event: React.MouseEvent, node: TreeNode) => void;
}) {
  return (
    <ul className="workspace-tree">
      {nodes.map((node) => (
        <li key={node.path}>
          <button
            className={node.path === active ? "active" : ""}
            onClick={() => (node.is_dir ? onExpand(node) : onOpen(node.path))}
            onContextMenu={(event) => {
              event.preventDefault();
              onContext(event, node);
            }}
            title={`${node.path} — right-click for file actions`}
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
              onContext={onContext}
            />
          ) : null}
        </li>
      ))}
    </ul>
  );
}

// The target of a right-click context menu: a workspace tree node or a flat
// scratch file. x/y are viewport coords so the menu can be positioned from a
// fixed element regardless of which dock the tree sits in.
type MenuTarget = {
  x: number;
  y: number;
  path: string;
  name: string;
  isDir: boolean;
  // When true the target is a scratch file (its path is its bare name).
  scratch: boolean;
};

function ContextMenu({
  target,
  disabled,
  onAction,
  onClose,
}: {
  target: MenuTarget;
  disabled: boolean;
  onAction: (action: string) => void;
  onClose: () => void;
}) {
  useEffect(() => {
    const close = () => onClose();
    window.addEventListener("click", close);
    window.addEventListener("blur", close);
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", key);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("blur", close);
      window.removeEventListener("keydown", key);
    };
  }, [onClose]);

  const items: { action: string; label: string }[] = [];
  if (target.isDir && !target.scratch) {
    items.push({ action: "new-file", label: "New file" });
    items.push({ action: "new-folder", label: "New folder" });
  }
  if (target.scratch || target.isDir) {
    items.push({ action: "new-scratch", label: "New scratch" });
  }
  items.push({ action: "rename", label: "Rename" });
  items.push({ action: "duplicate", label: "Duplicate" });
  items.push({ action: "delete", label: "Delete" });

  return createPortal(
    <div
      className="workspace-context-menu"
      role="menu"
      style={{
        left: Math.min(target.x, window.innerWidth - 200),
        top: Math.min(target.y, window.innerHeight - 180),
      }}
      onMouseDown={(event) => event.stopPropagation()}
    >
      {items.map((item) => (
        <button
          disabled={disabled}
          key={item.action}
          onClick={() => onAction(item.action)}
          role="menuitem"
        >
          {item.label}
        </button>
      ))}
    </div>,
    document.body,
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
  onTerminalPresenceChange,
  onTerminalPanelClose,
  onShowDocks,
  model,
  models,
  layoutPersistence,
  theme,
  vividness,
  scratchDir,
}: Props) {
  const [tree, setTree] = useState<TreeNode[]>([]);
  const [files, setFiles] = useState<OpenFile[]>([]);
  const [activePath, setActivePath] = useState("");
  const [scratchFiles, setScratchFiles] = useState<WorkspaceEntry[]>([]);
  // The active right-click menu, or null when none is open.
  const [menu, setMenu] = useState<MenuTarget | null>(null);
  // The file manager's naming/confirmation dialogs. window.prompt and
  // window.confirm are no-ops in the Wails webview, so these are real modals.
  type NameRequest = {
    title: string;
    initial?: string;
    confirmLabel: string;
    onConfirm: (name: string) => void;
  };
  const [nameRequest, setNameRequest] = useState<NameRequest | null>(null);
  type ConfirmRequest = {
    title: string;
    message: string;
    confirmLabel: string;
    onConfirm: () => void;
  };
  const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest | null>(
    null,
  );
  const [git, setGit] = useState<GitStatusResult | null>(null);
  const [gitTabs, setGitTabs] = useState<GitTab[]>([]);
  // A non-empty id means the editor area is showing a diff rather than a file.
  const [activeGitTab, setActiveGitTab] = useState("");
  // Bumped on every index or working-tree change so open diffs re-read.
  const [gitRevision, setGitRevision] = useState(0);
  const [multiRun, setMultiRun] = useState(false);
  const storageKeys = dockStorageKeys(layoutPersistence, workDir);
  const [columns, setColumns] = useState<DockColumns>(() =>
    loadRuntimeColumns(workDir, storageKeys?.layout ?? null),
  );
  // The tool being dragged, and the zone the pointer is over, as
  // `${groupID}:${where}` — only used to light up drop targets.
  const [dragging, setDragging] = useState("");
  const [dropHint, setDropHint] = useState("");
  const [terminalSlot, setTerminalSlot] = useState<HTMLElement | null>(null);
  // Seeded from the restored layout, not from one: a saved Terminal 1 must not
  // be handed the same id twice.
  const nextTerminal = useRef(0);
  const [quickOpen, setQuickOpen] = useState(false);
  const [lightEditorBackground, setLightEditorBackground] = useState(false);
  const [scratchExpanded, setScratchExpanded] = useState(
    DEFAULT_SCRATCH_EXPANDED,
  );
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

  const collapseAll = () => {
    setTree((current) => collapseTree(current));
  };

  // Reloads the top-level tree and the quick-open index. Called after a file
  // operation so the explorer reflects what just changed without resetting the
  // open editors.
  const refreshTree = useCallback(
    (generation: number): Promise<void> =>
      Promise.all([
        forge.listWorkspaceDir("").then((next) => {
          if (generation === workspaceGeneration.current) setTree(next);
        }),
        indexWorkspace(generation),
      ])
        .then(() => {
          refreshGit();
        })
        .then(() => undefined),
    [indexWorkspace, onNotify, refreshGit],
  );

  // Reloads the scratch file list so the explorer reflects a scratch
  // mutation. Workspaces are irrelevant to scratch files, so no generation
  // guard is needed: the list is cheap and idempotent.
  const refreshScratch = useCallback(() => {
    void forge
      .listScratch()
      .then(setScratchFiles)
      .catch((error: unknown) => onNotify(String(error)));
  }, [onNotify]);

  useEffect(() => {
    refreshScratch();
  }, [refreshScratch, scratchDir]);

  // Joins a parent directory (already relative to the workspace) with a user
  // name to make a workspace-relative path. Escaping names are rejected rather
  // than silently truncated.
  const nameToPath = (parent: string, name: string): string | null => {
    const trimmed = name.trim();
    if (!trimmed) return null;
    if (trimmed === "." || trimmed === ".." || trimmed.includes("/"))
      return null;
    return parent ? `${parent}/${trimmed}` : trimmed;
  };

  // Prompts for a scratch-file name and, if valid, creates and opens it.
  const newScratch = () => {
    setNameRequest({
      title: "New scratch file",
      confirmLabel: "Create",
      onConfirm: (name) => {
        setNameRequest(null);
        void forge
          .createScratchFile(name, "")
          .then(() => refreshScratch())
          .then(() => openFile(name, true))
          .catch((error: unknown) => onNotify(String(error)));
      },
    });
  };

  // Creates a new empty file in parent (relative path, "" for the root) and
  // opens it for editing.
  const newFile = (parent: string) => {
    setNameRequest({
      title: "New file",
      confirmLabel: "Create",
      onConfirm: (name) => {
        setNameRequest(null);
        const path = nameToPath(parent, name);
        if (!path) return;
        void forge
          .createWorkspaceFile(path, "")
          .then(() => openFile(path))
          .then(() =>
            refreshTree(++workspaceGeneration.current).catch((error: unknown) =>
              onNotify(String(error)),
            ),
          )
          .catch((error: unknown) => onNotify(String(error)));
      },
    });
  };

  // Creates a new folder under parent.
  const newFolder = (parent: string) => {
    setNameRequest({
      title: "New folder",
      confirmLabel: "Create",
      onConfirm: (name) => {
        setNameRequest(null);
        const path = nameToPath(parent, name);
        if (!path) return;
        void forge
          .createWorkspaceDir(path)
          .then(() =>
            refreshTree(++workspaceGeneration.current).catch((error: unknown) =>
              onNotify(String(error)),
            ),
          )
          .catch((error: unknown) => onNotify(String(error)));
      },
    });
  };

  const runMenuAction = (menuAction: string) => {
    if (!menu) return;
    const target = menu;
    const close = () => setMenu(null);
    if (menuAction === "new-file") newFile(target.path);
    else if (menuAction === "new-folder") newFolder(target.path);
    else if (menuAction === "new-scratch") newScratch();
    else if (menuAction === "duplicate") duplicateTarget(target);
    else if (menuAction === "rename") renameTarget(target);
    else if (menuAction === "delete") deleteTarget(target);
    close();
  };

  // Duplicates a workspace path or scratch file, asking for the copy's name.
  const duplicateTarget = (target: MenuTarget) => {
    const base = target.name.replace(/\.[^.]*$/, "");
    const defaultName = `${base} copy${target.isDir ? "" : (target.name.match(/\.[^.]*$/)?.[0] ?? "")}`;
    setNameRequest({
      title: "Duplicate",
      initial: defaultName,
      confirmLabel: "Duplicate",
      onConfirm: (name) => {
        setNameRequest(null);
        if (target.scratch) {
          if (name === target.name) {
            onNotify("Choose a different scratch file name");
            return;
          }
          void forge
            .copyScratchFile(target.name, name)
            .then(() => refreshScratch())
            .catch((error: unknown) => onNotify(String(error)));
          return;
        }
        const parent = target.path.includes("/")
          ? target.path.slice(0, target.path.lastIndexOf("/"))
          : "";
        const to = nameToPath(parent, name);
        if (!to) return;
        void forge
          .copyWorkspacePath(target.path, to)
          .then(() =>
            refreshTree(++workspaceGeneration.current).catch((error: unknown) =>
              onNotify(String(error)),
            ),
          )
          .catch((error: unknown) => onNotify(String(error)));
      },
    });
  };

  // Renames a workspace path or scratch file.
  const renameTarget = (target: MenuTarget) => {
    setNameRequest({
      title: "Rename",
      initial: target.name,
      confirmLabel: "Rename",
      onConfirm: (name) => {
        setNameRequest(null);
        if (target.scratch) {
          if (name === target.name) {
            onNotify("Choose a different scratch file name");
            return;
          }
          void forge
            .renameScratchFile(target.name, name)
            .then(() => {
              const oldKey = `${SCRATCH_PREFIX}${target.name}`;
              const newKey = `${SCRATCH_PREFIX}${name}`;
              setFiles((current) =>
                current.map((open) =>
                  open.scratch && open.path === oldKey
                    ? { ...open, path: newKey }
                    : open,
                ),
              );
              if (activePath === oldKey) setActivePath(newKey);
              refreshScratch();
            })
            .catch((error: unknown) => onNotify(String(error)));
          return;
        }
        const parent = target.path.includes("/")
          ? target.path.slice(0, target.path.lastIndexOf("/"))
          : "";
        const to = nameToPath(parent, name);
        if (!to) return;
        void forge
          .renameWorkspacePath(target.path, to)
          .then(() => {
            // Keep the editor open on the renamed file: re-read it under its new
            // path so the tab, syntax highlighting and save target follow.
            const open = files.find(
              (candidate) =>
                !candidate.scratch && candidate.path === target.path,
            );
            if (open) {
              void forge.readWorkspaceFile(to).then((next) => {
                setFiles((current) =>
                  current.map((candidate) =>
                    !candidate.scratch && candidate.path === target.path
                      ? toOpenFile(next)
                      : candidate,
                  ),
                );
                if (activePath === target.path) setActivePath(to);
              });
            }
            return refreshTree(++workspaceGeneration.current).catch(
              (error: unknown) => onNotify(String(error)),
            );
          })
          .catch((error: unknown) => onNotify(String(error)));
      },
    });
  };

  // Deletes a workspace path or scratch file after confirmation.
  const deleteTarget = (target: MenuTarget) => {
    setConfirmRequest({
      title: "Delete",
      message: `Delete ${target.name}${target.isDir ? " and its contents" : ""}?`,
      confirmLabel: "Delete",
      onConfirm: () => {
        setConfirmRequest(null);
        if (target.scratch) {
          const key = `${SCRATCH_PREFIX}${target.name}`;
          void forge
            .deleteScratchFile(target.name)
            .then(() => {
              setFiles((current) =>
                current.filter((open) => open.path !== key),
              );
              if (activePath === key) setActivePath("");
              refreshScratch();
            })
            .catch((error: unknown) => onNotify(String(error)));
          return;
        }
        void forge
          .deleteWorkspacePath(target.path)
          .then(() => {
            setFiles((current) =>
              current.filter((open) => open.path !== target.path),
            );
            if (activePath === target.path) setActivePath("");
            refreshTree(++workspaceGeneration.current).catch((error: unknown) =>
              onNotify(String(error)),
            );
          })
          .catch((error: unknown) => onNotify(String(error)));
      },
    });
  };

  // Open-file paths are namespaced so a scratch file and a workspace file that
  // share a name never collide in the tab strip or the active-path lookup.
  const SCRATCH_PREFIX = "scratch:";
  const scratchOpenName = (file: OpenFile) =>
    file.scratch ? file.path.slice(SCRATCH_PREFIX.length) : file.path;

  const openFile = useCallback(
    (path: string, scratch = false) => {
      setActiveGitTab("");
      focusTool("editor");
      const key = scratch ? `${SCRATCH_PREFIX}${path}` : path;
      if (
        files.some(
          (candidate) =>
            candidate.path === key && candidate.scratch === scratch,
        )
      ) {
        openRequest.current++;
        setActivePath(key);
        setQuickOpen(false);
        return;
      }
      const request = ++openRequest.current;
      const generation = workspaceGeneration.current;
      const read = scratch
        ? forge.readScratchFile(path)
        : forge.readWorkspaceFile(path);
      void read
        .then((next) => {
          if (!scratch && generation !== workspaceGeneration.current) return;
          setFiles((current) =>
            current.some(
              (candidate) =>
                candidate.path === key && candidate.scratch === scratch,
            )
              ? current
              : [...current, { ...toOpenFile(next), path: key, scratch }],
          );
          if (request === openRequest.current) setActivePath(key);
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
    if (activePath === path) {
      const next = remaining[Math.min(index, remaining.length - 1)]?.path ?? "";
      setActivePath(next);
      // With no file left the editor has nothing to show, so hand the dock
      // back to the explorer instead of leaving an empty editor on screen.
      if (!next && remaining.length === 0) focusTool("explorer");
    }
  };

  const save = useCallback(() => {
    if (!file || !isDirty(file)) return;
    const expectedVersion = file.version;
    const save = file.scratch
      ? forge.writeScratchFile(
          scratchOpenName(file),
          file.content,
          file.version,
        )
      : forge.writeWorkspaceFile(file.path, file.content, file.version);
    void save
      .then((saved) => {
        setFiles((current) =>
          current.map((candidate) =>
            candidate.path === file.path && candidate.scratch === file.scratch
              ? acceptSavedFile(candidate, saved, expectedVersion)
              : candidate,
          ),
        );
        refreshGit();
        onNotify(`saved ${scratchOpenName(file)}`);
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

  useEffect(
    () => saveDockWidths(dockWidths, storageKeys?.widths ?? null),
    [dockWidths, storageKeys?.widths],
  );

  useEffect(() => {
    setTerminalSlot(document.getElementById("forge-terminal-button"));
  }, []);

  useEffect(
    () => saveRuntimeColumns(workDir, columns, storageKeys?.layout ?? null),
    [columns, storageKeys?.layout, workDir],
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
    if (findTool(columns, id)?.tool.kind === "terminal") {
      onTerminalPanelClose(workDir, id);
    }
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
            <span className="workspace-root-actions">
              <button
                className="icon-btn"
                onClick={collapseAll}
                title="Collapse all folders"
                aria-label="Collapse all folders"
              >
                ⟲
              </button>
              <span className="workspace-root-actions-sep" aria-hidden="true" />
              <button
                className="icon-btn"
                disabled={Boolean(browsing)}
                onClick={() => newFile("")}
                title={
                  browsing
                    ? "Browsing is read-only"
                    : "New file in this workspace"
                }
                aria-label="New file"
              >
                New file
              </button>
              <button
                className="icon-btn"
                onClick={newScratch}
                title="New scratch file"
                aria-label="New scratch file"
              >
                New scratch
              </button>
            </span>
          </div>
          <FileTree
            nodes={tree}
            active={activePath}
            onOpen={openFile}
            onExpand={expand}
            onContext={(event, node) =>
              setMenu({
                x: event.clientX,
                y: event.clientY,
                path: node.path,
                name: node.name,
                isDir: node.is_dir,
                scratch: false,
              })
            }
          />
          <button
            className="scratch-head"
            onClick={() => setScratchExpanded((expanded) => !expanded)}
            aria-expanded={scratchExpanded}
          >
            <span aria-hidden="true">{scratchExpanded ? "▾" : "▸"}</span>
            scratch
          </button>
          {scratchExpanded ? (
            <ul className="workspace-tree scratch-tree">
              {scratchFiles.map((entry) => {
                const key = `${SCRATCH_PREFIX}${entry.path}`;
                return (
                  <li key={entry.path}>
                    <button
                      className={key === activePath ? "active" : ""}
                      onClick={() => openFile(entry.path, true)}
                      onContextMenu={(event) => {
                        event.preventDefault();
                        setMenu({
                          x: event.clientX,
                          y: event.clientY,
                          path: entry.path,
                          name: entry.name,
                          isDir: entry.is_dir,
                          scratch: true,
                        });
                      }}
                      title={`${entry.name} — right-click for scratch actions`}
                    >
                      <span aria-hidden="true">·</span>
                      {entry.name}
                    </button>
                  </li>
                );
              })}
              {scratchFiles.length === 0 ? (
                <li className="workspace-tree-empty">
                  <button onClick={newScratch}>Create a scratch file</button>
                </li>
              ) : null}
            </ul>
          ) : null}
          {menu ? (
            <ContextMenu
              target={menu}
              disabled={Boolean(browsing)}
              onAction={runMenuAction}
              onClose={() => setMenu(null)}
            />
          ) : null}
          {nameRequest ? (
            <NameDialog
              title={nameRequest.title}
              initial={nameRequest.initial}
              confirmLabel={nameRequest.confirmLabel}
              onConfirm={nameRequest.onConfirm}
              onCancel={() => setNameRequest(null)}
            />
          ) : null}
          {confirmRequest ? (
            <ConfirmDialog
              title={confirmRequest.title}
              message={confirmRequest.message}
              confirmLabel={confirmRequest.confirmLabel}
              onConfirm={confirmRequest.onConfirm}
              onCancel={() => setConfirmRequest(null)}
            />
          ) : null}
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
          theme={theme}
          vividness={vividness}
          onNotify={onNotify}
          onPresenceChange={onTerminalPresenceChange}
        />
      );
    const diffTab = gitTabs.find((tab) => tab.id === activeGitTab) ?? null;
    return (
      <section className="workspace-editor">
        {files.length > 0 || gitTabs.length > 0 ? (
          <div className="workspace-tabs">
            {files.map((open) => {
              const openName = open.scratch
                ? open.path.slice("scratch:".length)
                : open.path;
              return (
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
                    {openName.split("/").pop()}
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
              );
            })}
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
            {diffTab
              ? diffTab.title
              : file
                ? file.scratch
                  ? file.path.slice("scratch:".length)
                  : file.path
                : "No file open"}
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
          {file ? (
            <>
              <button disabled={!dirty} onClick={revert}>
                Revert
              </button>
              <button disabled={!dirty} onClick={save}>
                Save
              </button>
            </>
          ) : null}
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
            path={file.scratch ? file.path.slice("scratch:".length) : file.path}
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
