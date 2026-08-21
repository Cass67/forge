import { FitAddon } from "@xterm/addon-fit";
import { Terminal, type ITheme } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { forge } from "../bridge";
import {
  closeTerminalPane,
  loadTerminalWorkspace,
  newTerminalTab,
  resizeTerminalSplit,
  splitTerminal,
  splitTerminalWithLayout,
  terminalID,
  terminalIDs,
  type TerminalLayout,
  type TerminalSplit,
  type TerminalTab,
  type TerminalWorkspaceState,
} from "../terminalLayout";

const TERMINAL_TAB_MIME = "application/x-forge-terminal-tab";

type Props = {
  workDir: string;
  instanceID?: string;
  onNotify: (message: string) => void;
};

function terminalFontSize(): number {
  return Number.parseFloat(getComputedStyle(document.documentElement).fontSize) * 0.8125;
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

function TerminalPane({
  id,
  active,
  visible,
  onActivate,
  onNotify,
  onDropTab,
}: {
  id: string;
  active: boolean;
  visible: boolean;
  onActivate: () => void;
  onNotify: (message: string) => void;
  onDropTab: (tab: string, pane: string, direction: TerminalSplit["direction"], before: boolean) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const fit = useRef<FitAddon | null>(null);
  const started = useRef(false);
  const lastInput = useRef({ data: "", at: 0 });

  useEffect(() => {
    if (!host.current) return;
    const term = new Terminal({
      allowProposedApi: false,
      convertEol: false,
      cursorBlink: true,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
      fontSize: terminalFontSize(),
      scrollback: 5000,
      theme: terminalTheme(),
    });
    const fitAddon = new FitAddon();
    terminal.current = term;
    fit.current = fitAddon;
    term.loadAddon(fitAddon);
    term.open(host.current);
    fitAddon.fit();

    const offEvent = forge.onTerminal((event) => {
      if (event.id !== id) return;
      if (event.data) term.write(event.data);
      if (event.closed) {
        started.current = false;
        term.writeln("\r\n[process exited]");
      }
    });
    const input = term.onData((data) => {
      // Wails/WebKit can dispatch the same xterm input callback twice in one
      // event turn after a portal remount. Do not send that duplicate to PTY.
      const now = performance.now();
      if (lastInput.current.data === data && now - lastInput.current.at < 8) return;
      lastInput.current = { data, at: now };
      void forge
        .writeTerminal(id, data)
        .catch((error: unknown) => onNotify(String(error)));
    });
    const resize = () => {
      if (
        !host.current ||
        host.current.clientWidth < 2 ||
        host.current.clientHeight < 2
      )
        return;
      fitAddon.fit();
      if (started.current) {
        void forge
          .resizeTerminal(id, term.rows, term.cols)
          .catch((error: unknown) => onNotify(String(error)));
      }
    };
    const observer = new ResizeObserver(resize);
    observer.observe(host.current);
    const themeObserver = new MutationObserver(() => {
      term.options.theme = terminalTheme();
      term.options.fontSize = terminalFontSize();
      resize();
    });
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme", "style"],
    });
    void forge
      .startTerminal(id, term.rows, term.cols)
      .then(() => {
        started.current = true;
        resize();
      })
      .catch((error: unknown) => onNotify(String(error)));

    return () => {
      observer.disconnect();
      themeObserver.disconnect();
      input.dispose();
      offEvent();
      started.current = false;
      void forge.closeTerminal(id);
      term.dispose();
      terminal.current = null;
      fit.current = null;
    };
  }, [id, onNotify]);

  useEffect(() => {
    if (!visible) return;
    requestAnimationFrame(() => {
      fit.current?.fit();
      if (active) terminal.current?.focus();
    });
  }, [active, visible]);

  return (
    <div
      className={`terminal-pane ${active ? "active" : ""}`}
      onPointerDown={onActivate}
      onDragOver={(event) => {
        if (!event.dataTransfer.types.includes(TERMINAL_TAB_MIME)) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
      }}
      onDrop={(event) => {
        const tab = event.dataTransfer.getData(TERMINAL_TAB_MIME);
        if (!tab) return;
        event.preventDefault();
        const rect = event.currentTarget.getBoundingClientRect();
        const x = event.clientX - rect.left;
        const y = event.clientY - rect.top;
        const horizontalEdge = Math.min(x, rect.width - x) / rect.width;
        const verticalEdge = Math.min(y, rect.height - y) / rect.height;
        const direction = horizontalEdge < verticalEdge ? "horizontal" : "vertical";
        const before = direction === "horizontal" ? x < rect.width / 2 : y < rect.height / 2;
        onDropTab(tab, id, direction, before);
      }}
    >
      <div ref={host} className="terminal-host" />
    </div>
  );
}

function SplitView({
  layout,
  activePane,
  visible,
  onActivate,
  onRatio,
  onNotify,
  onDropTab,
}: {
  layout: TerminalLayout;
  activePane: string;
  visible: boolean;
  onActivate: (pane: string) => void;
  onRatio: (split: string, ratio: number) => void;
  onNotify: (message: string) => void;
  onDropTab: (tab: string, pane: string, direction: TerminalSplit["direction"], before: boolean) => void;
}) {
  const splitHost = useRef<HTMLDivElement>(null);
  if (layout.kind === "terminal") {
    return (
      <TerminalPane
        id={layout.id}
        active={activePane === layout.id}
        visible={visible}
        onActivate={() => onActivate(layout.id)}
        onNotify={onNotify}
        onDropTab={onDropTab}
      />
    );
  }
  const horizontal = layout.direction === "horizontal";
  const startDrag = (event: React.PointerEvent) => {
    event.preventDefault();
    const move = (pointer: PointerEvent) => {
      const rect = splitHost.current?.getBoundingClientRect();
      if (!rect) return;
      const ratio = horizontal
        ? (pointer.clientX - rect.left) / rect.width
        : (pointer.clientY - rect.top) / rect.height;
      onRatio(layout.id, ratio);
    };
    const stop = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", stop);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", stop);
  };
  return (
    <div
      ref={splitHost}
      className={`terminal-split ${horizontal ? "horizontal" : "vertical"}`}
    >
      <div style={{ flexBasis: `${layout.ratio * 100}%` }}>
        <SplitView
          layout={layout.first}
          activePane={activePane}
          visible={visible}
          onActivate={onActivate}
          onRatio={onRatio}
          onNotify={onNotify}
          onDropTab={onDropTab}
        />
      </div>
      <div
        className="terminal-divider"
        role="separator"
        aria-orientation={horizontal ? "vertical" : "horizontal"}
        onPointerDown={startDrag}
      />
      <div style={{ flexBasis: `${(1 - layout.ratio) * 100}%` }}>
        <SplitView
          layout={layout.second}
          activePane={activePane}
          visible={visible}
          onActivate={onActivate}
          onRatio={onRatio}
          onNotify={onNotify}
          onDropTab={onDropTab}
        />
      </div>
    </div>
  );
}

export function TerminalWorkspace({
  workDir,
  instanceID = "default",
  onNotify,
}: Props) {
  const storageKey = useMemo(
    () => `forge.terminals:${workDir}:${instanceID}`,
    [instanceID, workDir],
  );
  const [state, setState] = useState<TerminalWorkspaceState>(() =>
    loadTerminalWorkspace(storageKey),
  );
  const active =
    state.tabs.find((tab) => tab.id === state.activeTab) ?? state.tabs[0];

  useEffect(
    () => localStorage.setItem(storageKey, JSON.stringify(state)),
    [state, storageKey],
  );

  const updateTab = useCallback(
    (id: string, update: (tab: TerminalTab) => TerminalTab) => {
      setState((current) => ({
        ...current,
        tabs: current.tabs.map((tab) => (tab.id === id ? update(tab) : tab)),
      }));
    },
    [],
  );

  const addTab = () => {
    setState((current) => {
      const tab = newTerminalTab(current.tabs.length + 1);
      return { tabs: [...current.tabs, tab], activeTab: tab.id };
    });
  };
  const closeTab = (id: string) => {
    setState((current) => {
      const tabs = current.tabs.filter((tab) => tab.id !== id);
      if (tabs.length === 0) {
        const replacement = newTerminalTab(1);
        return { tabs: [replacement], activeTab: replacement.id };
      }
      return {
        tabs,
        activeTab: current.activeTab === id ? tabs[0].id : current.activeTab,
      };
    });
  };
  const split = (direction: TerminalSplit["direction"]) => {
    if (!active) return;
    const pane = terminalID();
    updateTab(active.id, (tab) => ({
      ...tab,
      activePane: pane,
      layout: splitTerminal(tab.layout, tab.activePane, direction, pane),
    }));
  };
  const closePane = () => {
    if (!active) return;
    const layout = closeTerminalPane(active.layout, active.activePane);
    if (!layout) return closeTab(active.id);
    updateTab(active.id, (tab) => ({
      ...tab,
      layout,
      activePane: terminalIDs(layout)[0],
    }));
  };
  const dropTab = (
    sourceID: string,
    targetPane: string,
    direction: TerminalSplit["direction"],
    before: boolean,
  ) => {
    setState((current) => {
      const source = current.tabs.find((tab) => tab.id === sourceID);
      const target = current.tabs.find((tab) => terminalIDs(tab.layout).includes(targetPane));
      if (!source || !target || source.id === target.id) return current;
      const tabs = current.tabs
        .filter((tab) => tab.id !== source.id)
        .map((tab) =>
          tab.id === target.id
            ? {
                ...tab,
                activePane: source.activePane,
                layout: splitTerminalWithLayout(tab.layout, targetPane, direction, source.layout, before),
              }
            : tab,
        );
      return { tabs, activeTab: target.id };
    });
  };

  return (
    <section className="terminal-workspace">
      <div className="terminal-tabs">
        {state.tabs.map((tab) => (
          <div
            key={tab.id}
            className={`terminal-tab ${tab.id === state.activeTab ? "active" : ""}`}
            draggable
            onDragStart={(event) => {
              event.dataTransfer.effectAllowed = "move";
              event.dataTransfer.setData(TERMINAL_TAB_MIME, tab.id);
            }}
          >
            <button
              className="terminal-tab-select"
              onClick={() =>
                setState((current) => ({ ...current, activeTab: tab.id }))
              }
            >
              {tab.title}
            </button>
            <button
              className="terminal-tab-close"
              onClick={() => closeTab(tab.id)}
              aria-label={`Close ${tab.title}`}
            >
              ×
            </button>
          </div>
        ))}
        <button onClick={addTab} title="New terminal tab">
          ＋
        </button>
        <span className="terminal-tab-spacer" />
        <button
          onClick={() => split("horizontal")}
          title="Split left and right"
        >
          Split H
        </button>
        <button onClick={() => split("vertical")} title="Split top and bottom">
          Split V
        </button>
        <button onClick={closePane} title="Close active terminal pane">
          Close pane
        </button>
      </div>
      <div className="terminal-tab-content">
        {state.tabs.map((tab) => (
          <div
            key={tab.id}
            className={`terminal-tab-page ${tab.id === state.activeTab ? "active" : ""}`}
          >
            <SplitView
              layout={tab.layout}
              activePane={tab.activePane}
              visible={tab.id === state.activeTab}
              onActivate={(pane) =>
                updateTab(tab.id, (current) => ({
                  ...current,
                  activePane: pane,
                }))
              }
              onRatio={(splitID, ratio) =>
                updateTab(tab.id, (current) => ({
                  ...current,
                  layout: resizeTerminalSplit(current.layout, splitID, ratio),
                }))
              }
              onNotify={onNotify}
              onDropTab={dropTab}
            />
          </div>
        ))}
      </div>
    </section>
  );
}
