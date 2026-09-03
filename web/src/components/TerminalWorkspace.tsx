import { FitAddon } from "xterm-addon-fit";
import { Terminal } from "xterm";
import "xterm/css/xterm.css";
import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { forge } from "../bridge";
import { terminalTheme, type Theme } from "../theme";
import {
  closePane,
  isPane,
  loadSplits,
  paneIDs,
  saveSplits,
  setRatio,
  splitPane,
  type SplitDirection,
  type SplitPath,
  type TerminalNode,
} from "../terminalSplit";

type Props = {
  workDir: string;
  instanceID?: string;
  layoutKey?: string;
  onNotify: (message: string) => void;
  onPresenceChange?: (workspace: string, id: string, open: boolean) => void;
  theme: Theme;
  vividness: number;
};

function terminalFontSize(): number {
  return (
    Number.parseFloat(getComputedStyle(document.documentElement).fontSize) *
    0.8125
  );
}

function currentTerminalTheme() {
  const styles = getComputedStyle(document.documentElement);
  return terminalTheme((token) =>
    styles.getPropertyValue(`--${token}`).trim(),
  );
}

// One shell. The panel above it decides how many there are and where they sit.
function TerminalPane({
  workDir,
  instanceID,
  layoutKey,
  onNotify,
  onFocus,
  active,
  onClose,
  closable,
  onPresenceChange,
  theme,
  vividness,
}: {
  workDir: string;
  instanceID: string;
  layoutKey: string;
  onNotify: (message: string) => void;
  onFocus: () => void;
  active: boolean;
  onClose: () => void;
  closable: boolean;
  onPresenceChange?: (workspace: string, id: string, open: boolean) => void;
  theme: Theme;
  vividness: number;
}) {
  const host = useRef<HTMLDivElement>(null);
  const resizeRef = useRef<() => void>(() => {});
  const terminalRef = useRef<Terminal | null>(null);

  useEffect(() => {
    const element = host.current;
    // Restored dock tools render before backend Init supplies workspace. Wait
    // for readiness; changing workDir then starts a fresh workspace-bound PTY.
    if (!element || !workDir) return;
    element.replaceChildren();
    // Stable IDs let the backend reattach this pane after its workspace UI is
    // unmounted while another workspace is active.
    const id = `${instanceID}:pty`;
    let disposed = false;
    let started = false;
    let pendingInput = "";
    let resizeFrame = 0;
    let backendRows = 0;
    let backendCols = 0;
    const term = new Terminal({
      allowProposedApi: false,
      convertEol: false,
      cursorBlink: true,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
      fontSize: terminalFontSize(),
      scrollback: 5000,
      theme: currentTerminalTheme(),
    });
    terminalRef.current = term;
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(element);

    const resize = () => {
      if (resizeFrame) return;
      resizeFrame = requestAnimationFrame(() => {
        resizeFrame = 0;
        if (element.closest(".terminal-workspace.resizing")) return;
        if (element.clientWidth < 2 || element.clientHeight < 2) return;
        fit.fit();
        if (
          started &&
          (term.rows !== backendRows || term.cols !== backendCols)
        ) {
          backendRows = term.rows;
          backendCols = term.cols;
          void forge
            .resizeTerminal(id, term.rows, term.cols)
            .catch((error: unknown) => onNotify(String(error)));
        }
      });
    };
    resizeRef.current = resize;
    const offEvent = forge.onTerminal((event) => {
      if (event.id !== id) return;
      if (event.data) term.write(event.data);
      if (event.closed) {
        started = false;
        onPresenceChange?.(workDir, id, false);
        term.writeln("\r\n[process exited]");
      }
    });
    const write = (data: string) => {
      void forge
        .writeTerminal(id, data)
        .catch((error: unknown) => onNotify(String(error)));
    };
    const input = term.onData((data) => {
      if (!started) {
        pendingInput += data;
        return;
      }
      write(data);
    });
    const observer = new ResizeObserver(resize);
    observer.observe(element);
    const workspace = element.closest(".terminal-workspace");
    workspace?.addEventListener("terminal-resize-end", resize);
    if (element.clientWidth >= 2 && element.clientHeight >= 2) fit.fit();
    backendRows = term.rows;
    backendCols = term.cols;
    void forge
      .startTerminal(id, term.rows, term.cols)
      .then((output) => {
        if (disposed) {
          void forge.closeTerminal(id);
          return;
        }
        started = true;
        if (output) term.write(output);
        onPresenceChange?.(workDir, id, true);
        resize();
        if (pendingInput) {
          write(pendingInput);
          pendingInput = "";
        }
      })
      .catch((error: unknown) => {
        if (!disposed) onNotify(String(error));
      });

    return () => {
      disposed = true;
      cancelAnimationFrame(resizeFrame);
      observer.disconnect();
      workspace?.removeEventListener("terminal-resize-end", resize);
      input.dispose();
      offEvent();
      started = false;
      resizeRef.current = () => {};
      terminalRef.current = null;
      term.dispose();
      element.replaceChildren();
    };
  }, [instanceID, onNotify, onPresenceChange, workDir]);

  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      const term = terminalRef.current;
      if (!term) return;
      term.options.theme = currentTerminalTheme();
      term.options.fontSize = terminalFontSize();
      resizeRef.current();
    });
    return () => cancelAnimationFrame(frame);
  }, [theme, vividness]);

  useEffect(() => {
    const frame = requestAnimationFrame(() => resizeRef.current());
    return () => cancelAnimationFrame(frame);
  }, [layoutKey]);

  return (
    <div
      className={`terminal-pane ${active ? "active" : ""}`}
      onFocus={onFocus}
      onMouseDown={onFocus}
    >
      <div ref={host} className="terminal-host" />
      {closable ? (
        <button
          aria-label="Close this shell"
          className="terminal-pane-close"
          onClick={onClose}
          title="Close this shell"
        >
          ×
        </button>
      ) : null}
    </div>
  );
}

// Split-tree changes insert and remove wrapper elements around panes. Keeping
// each pane in a detached, stable root lets that root move to its new slot
// without unmounting TerminalPane, closing its PTY, or losing xterm scrollback.
function TerminalPaneHost({
  target,
  ...pane
}: React.ComponentProps<typeof TerminalPane> & {
  target: HTMLDivElement | null;
}) {
  const [root] = useState(() => document.createElement("div"));

  useLayoutEffect(() => {
    root.className = "terminal-pane-mount";
    target?.appendChild(root);
  }, [root, target]);

  useEffect(() => () => root.remove(), [root]);

  return createPortal(<TerminalPane {...pane} />, root);
}

// TerminalWorkspace is the panel: the shells inside it, the splits between
// them, and the two buttons that make more.
export function TerminalWorkspace({
  workDir,
  instanceID = "default",
  layoutKey = "",
  onNotify,
  onPresenceChange,
  theme,
  vividness,
}: Props) {
  const storageID = `${workDir}:${instanceID}`;
  const [tree, setTree] = useState<TerminalNode>(() =>
    loadSplits(storageID, `${instanceID}:1`),
  );
  const [activePane, setActivePane] = useState(() => paneIDs(tree)[0]);
  const nextPane = useRef(1);
  const frameRef = useRef<HTMLDivElement>(null);
  const [paneTargets, setPaneTargets] = useState<
    Record<string, HTMLDivElement | null>
  >({});
  const paneTargetRefs = useRef(
    new Map<string, (element: HTMLDivElement | null) => void>(),
  );
  const paneTargetRef = (id: string) => {
    const cached = paneTargetRefs.current.get(id);
    if (cached) return cached;
    const assign = (element: HTMLDivElement | null) =>
      setPaneTargets((current) =>
        current[id] === element ? current : { ...current, [id]: element },
      );
    paneTargetRefs.current.set(id, assign);
    return assign;
  };

  useEffect(() => saveSplits(storageID, tree), [storageID, tree]);

  // A pane that has gone leaves the focus on something that still exists.
  useEffect(() => {
    const live = paneIDs(tree);
    if (!live.includes(activePane)) setActivePane(live[0]);
  }, [activePane, tree]);

  const split = (dir: SplitDirection) => {
    setTree((current) => {
      const taken = new Set(paneIDs(current));
      let id = "";
      do {
        id = `${instanceID}:${++nextPane.current}`;
      } while (taken.has(id));
      setActivePane(id);
      return splitPane(current, activePane, dir, id);
    });
  };

  const close = (id: string) => {
    void forge.closeTerminal(`${id}:pty`);
    onPresenceChange?.(workDir, `${id}:pty`, false);
    setTree((current) => closePane(current, id));
  };

  // A divider drags along its own axis, measured against the panel so a nested
  // split still tracks the pointer.
  const startDrag =
    (path: SplitPath, dir: SplitDirection, element: HTMLElement | null) =>
    (event: React.PointerEvent) => {
      event.preventDefault();
      const split = element?.parentElement;
      const first = element?.previousElementSibling as HTMLElement | null;
      const second = element?.nextElementSibling as HTMLElement | null;
      const box = split?.getBoundingClientRect();
      if (!box || !first || !second) return;
      const workspace = frameRef.current;
      workspace?.classList.add("resizing");
      let frame = 0;
      let nextRatio: number | null = null;
      let appliedRatio: number | null = null;
      const apply = () => {
        frame = 0;
        if (nextRatio === null) return;
        const ratio = Math.max(0.1, Math.min(0.9, nextRatio));
        nextRatio = null;
        appliedRatio = ratio;
        first.style.flex = `${ratio} 1 0px`;
        second.style.flex = `${1 - ratio} 1 0px`;
      };
      const move = (pointer: PointerEvent) => {
        nextRatio =
          dir === "row"
            ? (pointer.clientX - box.left) / box.width
            : (pointer.clientY - box.top) / box.height;
        if (!frame) frame = requestAnimationFrame(apply);
      };
      const stop = () => {
        if (frame) {
          cancelAnimationFrame(frame);
          apply();
        }
        if (appliedRatio !== null) {
          const ratio = appliedRatio;
          setTree((current) => setRatio(current, path, ratio));
        }
        workspace?.classList.remove("resizing");
        requestAnimationFrame(() =>
          workspace?.dispatchEvent(new Event("terminal-resize-end")),
        );
        window.removeEventListener("pointermove", move);
        window.removeEventListener("pointerup", stop);
        window.removeEventListener("pointercancel", stop);
      };
      window.addEventListener("pointermove", move);
      window.addEventListener("pointerup", stop);
      window.addEventListener("pointercancel", stop);
    };

  const render = (node: TerminalNode, path: SplitPath = []): ReactNode => {
    if (isPane(node)) {
      return (
        <div
          className="terminal-pane-slot"
          key={node.id}
          ref={paneTargetRef(node.id)}
        />
      );
    }
    return (
      <div
        className={`terminal-split ${node.dir}`}
        key={path.join(".") || "root"}
      >
        <div style={{ flex: `${node.ratio} 1 0` }} className="terminal-branch">
          {render(node.first, [...path, "first"])}
        </div>
        <div
          className={`terminal-divider ${node.dir}`}
          onPointerDown={(event) =>
            startDrag(path, node.dir, event.currentTarget)(event)
          }
          role="separator"
        />
        <div
          style={{ flex: `${1 - node.ratio} 1 0` }}
          className="terminal-branch"
        >
          {render(node.second, [...path, "second"])}
        </div>
      </div>
    );
  };

  return (
    <section className="terminal-workspace" ref={frameRef}>
      <div className="terminal-actions">
        <button
          className="terminal-split-btn"
          onClick={() => split("row")}
          title="Split this shell to the right"
        >
          ⊞ right
        </button>
        <button
          className="terminal-split-btn"
          onClick={() => split("col")}
          title="Split this shell downwards"
        >
          ⊟ down
        </button>
      </div>
      <div className="terminal-tree">{render(tree)}</div>
      {paneIDs(tree).map((id) => (
        <TerminalPaneHost
          active={paneIDs(tree).length > 1 && id === activePane}
          closable={paneIDs(tree).length > 1}
          instanceID={id}
          key={id}
          layoutKey={layoutKey}
          onClose={() => close(id)}
          onFocus={() => setActivePane(id)}
          onNotify={onNotify}
          onPresenceChange={onPresenceChange}
          target={paneTargets[id] ?? null}
          theme={theme}
          vividness={vividness}
          workDir={workDir}
        />
      ))}
    </section>
  );
}
