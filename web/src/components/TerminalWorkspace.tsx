import { FitAddon } from "xterm-addon-fit";
import { Terminal, type ITheme } from "xterm";
import "xterm/css/xterm.css";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { forge } from "../bridge";
import {
  closePane,
  isPane,
  loadSplits,
  paneIDs,
  saveSplits,
  setRatio,
  splitPane,
  type SplitDirection,
  type TerminalNode,
} from "../terminalSplit";

type Props = {
  workDir: string;
  instanceID?: string;
  layoutKey?: string;
  onNotify: (message: string) => void;
};

function terminalFontSize(): number {
  return (
    Number.parseFloat(getComputedStyle(document.documentElement).fontSize) *
    0.8125
  );
}

function terminalTheme(): ITheme {
  const styles = getComputedStyle(document.documentElement);
  const color = (token: string) => styles.getPropertyValue(token).trim();
  const background = color("--bg");
  const foreground = color("--text");
  const accent = color("--accent");
  const muted = color("--muted");
  return {
    background,
    foreground,
    cursor: accent,
    cursorAccent: background,
    selectionBackground: color("--selected"),
    black: color("--panel"),
    red: color("--err"),
    green: color("--ok"),
    yellow: color("--warn"),
    blue: accent,
    magenta: accent,
    cyan: accent,
    white: foreground,
    brightBlack: muted,
    brightRed: color("--err"),
    brightGreen: color("--ok"),
    brightYellow: color("--warn"),
    brightBlue: accent,
    brightMagenta: accent,
    brightCyan: accent,
    brightWhite: foreground,
  };
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
}: {
  workDir: string;
  instanceID: string;
  layoutKey: string;
  onNotify: (message: string) => void;
  onFocus: () => void;
  active: boolean;
  onClose: () => void;
  closable: boolean;
}) {
  const host = useRef<HTMLDivElement>(null);
  const resizeRef = useRef<() => void>(() => {});

  useEffect(() => {
    const element = host.current;
    // Restored dock tools render before backend Init supplies workspace. Wait
    // for readiness; changing workDir then starts a fresh workspace-bound PTY.
    if (!element || !workDir) return;
    element.replaceChildren();
    const id = `${instanceID}:pty:${crypto.randomUUID()}`;
    let disposed = false;
    let started = false;
    let pendingInput = "";
    const term = new Terminal({
      allowProposedApi: false,
      convertEol: false,
      cursorBlink: true,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
      fontSize: terminalFontSize(),
      scrollback: 5000,
      theme: terminalTheme(),
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(element);

    const resize = () => {
      if (element.clientWidth < 2 || element.clientHeight < 2) return;
      fit.fit();
      if (started) {
        void forge
          .resizeTerminal(id, term.rows, term.cols)
          .catch((error: unknown) => onNotify(String(error)));
      }
    };
    resizeRef.current = resize;
    const offEvent = forge.onTerminal((event) => {
      if (event.id !== id) return;
      if (event.data) term.write(event.data);
      if (event.closed) {
        started = false;
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
    const themeObserver = new MutationObserver(() => {
      term.options.theme = terminalTheme();
      term.options.fontSize = terminalFontSize();
      resize();
    });
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme", "style"],
    });

    resize();
    void forge
      .startTerminal(id, term.rows, term.cols)
      .then(() => {
        if (disposed) {
          void forge.closeTerminal(id);
          return;
        }
        started = true;
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
      observer.disconnect();
      themeObserver.disconnect();
      input.dispose();
      offEvent();
      started = false;
      void forge.closeTerminal(id);
      resizeRef.current = () => {};
      term.dispose();
      element.replaceChildren();
    };
  }, [instanceID, onNotify, workDir]);

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

// TerminalWorkspace is the panel: the shells inside it, the splits between
// them, and the two buttons that make more.
export function TerminalWorkspace({
  workDir,
  instanceID = "default",
  layoutKey = "",
  onNotify,
}: Props) {
  const [tree, setTree] = useState<TerminalNode>(() =>
    loadSplits(instanceID, `${instanceID}:1`),
  );
  const [activePane, setActivePane] = useState(() => paneIDs(tree)[0]);
  const nextPane = useRef(1);
  const frameRef = useRef<HTMLDivElement>(null);

  useEffect(() => saveSplits(instanceID, tree), [instanceID, tree]);

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

  const close = (id: string) => setTree((current) => closePane(current, id));

  // A divider drags along its own axis, measured against the panel so a nested
  // split still tracks the pointer.
  const startDrag =
    (firstPane: string, dir: SplitDirection, element: HTMLElement | null) =>
    (event: React.PointerEvent) => {
      event.preventDefault();
      const box = element?.parentElement?.getBoundingClientRect();
      if (!box) return;
      const move = (pointer: PointerEvent) => {
        const ratio =
          dir === "row"
            ? (pointer.clientX - box.left) / box.width
            : (pointer.clientY - box.top) / box.height;
        setTree((current) => setRatio(current, firstPane, ratio));
      };
      const stop = () => {
        window.removeEventListener("pointermove", move);
        window.removeEventListener("pointerup", stop);
      };
      window.addEventListener("pointermove", move);
      window.addEventListener("pointerup", stop);
    };

  const render = (node: TerminalNode): ReactNode => {
    if (isPane(node)) {
      return (
        <TerminalPane
          active={paneIDs(tree).length > 1 && node.id === activePane}
          closable={paneIDs(tree).length > 1}
          instanceID={node.id}
          key={node.id}
          layoutKey={`${layoutKey}:${JSON.stringify(tree)}`}
          onClose={() => close(node.id)}
          onFocus={() => setActivePane(node.id)}
          onNotify={onNotify}
          workDir={workDir}
        />
      );
    }
    const first = paneIDs(node.first)[0];
    return (
      <div className={`terminal-split ${node.dir}`} key={first}>
        <div style={{ flex: `${node.ratio} 1 0` }} className="terminal-branch">
          {render(node.first)}
        </div>
        <div
          className={`terminal-divider ${node.dir}`}
          onPointerDown={(event) =>
            startDrag(first, node.dir, event.currentTarget)(event)
          }
          role="separator"
        />
        <div
          style={{ flex: `${1 - node.ratio} 1 0` }}
          className="terminal-branch"
        >
          {render(node.second)}
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
    </section>
  );
}
