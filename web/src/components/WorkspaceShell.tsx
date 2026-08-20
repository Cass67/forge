import { useCallback, useEffect, useRef, useState } from "react";
import {
  forge,
  type GitStatusResult,
  type WorkspaceEntry,
  type WorkspaceFile,
} from "../bridge";

type Props = { workDir: string; onNotify: (message: string) => void };
type TreeNode = WorkspaceEntry & { loaded?: boolean; expanded?: boolean; children?: TreeNode[] };

function patchTree(nodes: TreeNode[], path: string, patch: Partial<TreeNode>): TreeNode[] {
  return nodes.map((node) =>
    node.path === path
      ? { ...node, ...patch }
      : node.children
        ? { ...node, children: patchTree(node.children, path, patch) }
        : node,
  );
}

function FileTree({ nodes, active, onOpen, onExpand }: {
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
            onClick={() => node.is_dir ? onExpand(node) : onOpen(node.path)}
            title={node.path}
          >
            <span aria-hidden="true">{node.is_dir ? (node.expanded ? "▾" : "▸") : "·"}</span>
            {node.name}
          </button>
          {node.is_dir && node.expanded && node.children ? (
            <FileTree nodes={node.children} active={active} onOpen={onOpen} onExpand={onExpand} />
          ) : null}
        </li>
      ))}
    </ul>
  );
}

export function WorkspaceShell({ workDir, onNotify }: Props) {
  const [tree, setTree] = useState<TreeNode[]>([]);
  const [file, setFile] = useState<WorkspaceFile | null>(null);
  const [content, setContent] = useState("");
  const [git, setGit] = useState<GitStatusResult | null>(null);
  const [terminalOpen, setTerminalOpen] = useState(false);
  const [terminalText, setTerminalText] = useState("");
  const [terminalInput, setTerminalInput] = useState("");
  const terminalID = useRef(`workspace-${Date.now()}`);
  const outputRef = useRef<HTMLPreElement>(null);
  const dirty = file !== null && content !== file.content;

  const refreshGit = useCallback(() => {
    void forge.gitStatus().then(setGit).catch((error: unknown) => onNotify(String(error)));
  }, [onNotify]);

  useEffect(() => {
    setFile(null);
    setContent("");
    void forge.listWorkspaceDir("").then(setTree).catch((error: unknown) => onNotify(String(error)));
    refreshGit();
  }, [workDir, onNotify, refreshGit]);

  useEffect(() => forge.onTerminal((event) => {
    if (event.id !== terminalID.current) return;
    if (event.data) setTerminalText((text) => text + event.data);
    if (event.closed) setTerminalOpen(false);
  }), []);

  useEffect(() => {
    outputRef.current?.scrollTo({ top: outputRef.current.scrollHeight });
  }, [terminalText]);

  useEffect(() => () => {
    if (terminalOpen) void forge.closeTerminal(terminalID.current);
  }, [terminalOpen]);

  const expand = (node: TreeNode) => {
    if (node.loaded) {
      setTree((current) => patchTree(current, node.path, { expanded: !node.expanded }));
      return;
    }
    void forge.listWorkspaceDir(node.path)
      .then((children) => setTree((current) => patchTree(current, node.path, {
        children,
        expanded: true,
        loaded: true,
      })))
      .catch((error: unknown) => onNotify(String(error)));
  };

  const openFile = (path: string) => {
    if (dirty && !window.confirm("Discard unsaved changes?")) return;
    void forge.readWorkspaceFile(path)
      .then((next) => {
        setFile(next);
        setContent(next.content);
      })
      .catch((error: unknown) => onNotify(String(error)));
  };

  const save = () => {
    if (!file || !dirty) return;
    void forge.writeWorkspaceFile(file.path, content, file.version)
      .then((saved) => {
        setFile(saved);
        setContent(saved.content);
        refreshGit();
        onNotify(`saved ${saved.path}`);
      })
      .catch((error: unknown) => onNotify(String(error)));
  };

  const toggleTerminal = () => {
    if (terminalOpen) {
      void forge.closeTerminal(terminalID.current);
      setTerminalOpen(false);
      return;
    }
    setTerminalText("");
    void forge.startTerminal(terminalID.current, 24, 100)
      .then(() => setTerminalOpen(true))
      .catch((error: unknown) => onNotify(String(error)));
  };

  const submitTerminal = (event: React.FormEvent) => {
    event.preventDefault();
    if (!terminalOpen) return;
    void forge.writeTerminal(terminalID.current, `${terminalInput}\n`).catch((error: unknown) => onNotify(String(error)));
    setTerminalInput("");
  };

  return (
    <div className="workspace-shell">
      <aside className="workspace-explorer">
        <div className="workspace-panel-title">EXPLORER</div>
        <div className="workspace-root" title={workDir}>{workDir.split("/").pop() || workDir}</div>
        <FileTree nodes={tree} active={file?.path ?? ""} onOpen={openFile} onExpand={expand} />
      </aside>

      <section className="workspace-editor">
        <div className="workspace-toolbar">
          <span className="workspace-file-name">{file ? `${file.path}${dirty ? " ●" : ""}` : "No file open"}</span>
          <button disabled={!dirty} onClick={() => file && setContent(file.content)}>Revert</button>
          <button disabled={!dirty} onClick={save}>Save</button>
          <button onClick={toggleTerminal}>{terminalOpen ? "Close terminal" : "Terminal"}</button>
        </div>
        {file ? (
          <textarea
            className="workspace-textarea"
            aria-label={`Edit ${file.path}`}
            spellCheck={false}
            value={content}
            onChange={(event) => setContent(event.target.value)}
            onKeyDown={(event) => {
              if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
                event.preventDefault();
                save();
              }
            }}
          />
        ) : <div className="workspace-empty">Select a text file to edit</div>}
        {terminalOpen ? (
          <div className="workspace-terminal">
            <pre ref={outputRef}>{terminalText}</pre>
            <form onSubmit={submitTerminal}>
              <span>$</span>
              <input autoFocus value={terminalInput} onChange={(event) => setTerminalInput(event.target.value)} aria-label="Terminal input" />
            </form>
          </div>
        ) : null}
      </section>

      <aside className="workspace-git">
        <div className="workspace-panel-title">
          SOURCE CONTROL
          <button onClick={refreshGit} title="Refresh Git status">↻</button>
        </div>
        {!git?.repository ? <div className="workspace-muted">Not a Git repository</div> : (
          <>
            <div className="workspace-branch">{git.branch || "Git repository"}</div>
            {git.files.length === 0 ? <div className="workspace-muted">No changes</div> : (
              <ul>{git.files.map((entry) => (
                <li key={`${entry.status}-${entry.path}`}>
                  <button onClick={() => openFile(entry.path)} title={entry.path}>
                    <span>{entry.path}</span><b>{entry.status}</b>
                  </button>
                </li>
              ))}</ul>
            )}
          </>
        )}
      </aside>
    </div>
  );
}
