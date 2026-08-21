import { FitAddon } from "xterm-addon-fit";
import { Terminal, type ITheme } from "xterm";
import "xterm/css/xterm.css";
import { useEffect, useRef } from "react";
import { forge } from "../bridge";
import { TerminalInputGuard } from "../terminalInput";

type Props = {
  workDir: string;
  instanceID?: string;
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

export function TerminalWorkspace({
  workDir,
  instanceID = "default",
  onNotify,
}: Props) {
  const host = useRef<HTMLDivElement>(null);

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
    const inputGuard = new TerminalInputGuard();
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
    const helper = element.querySelector<HTMLTextAreaElement>(
      ".xterm-helper-textarea",
    );
    const blockReplayedText = (event: Event) => {
      // WKWebView can send delayed insertText with the helper's retained full
      // value after xterm already handled keydown. That replays whole commands.
      if (
        !(event instanceof InputEvent) ||
        event.inputType !== "insertText" ||
        event.isComposing
      )
        return;
      event.preventDefault();
      event.stopImmediatePropagation();
      if (helper) helper.value = "";
    };
    helper?.addEventListener("input", blockReplayedText, true);
    term.attachCustomKeyEventHandler((event) => {
      if (event.type !== "keydown" || event.isComposing) return true;
      const signature = `${event.code}:${event.key}:${event.metaKey}:${event.ctrlKey}:${event.altKey}:${event.shiftKey}`;
      const turn = inputGuard.startKey(signature, event.timeStamp);
      if (turn === 0) return false;
      setTimeout(() => inputGuard.endKey(turn), 0);
      return true;
    });

    const resize = () => {
      if (element.clientWidth < 2 || element.clientHeight < 2) return;
      fit.fit();
      if (started) {
        void forge
          .resizeTerminal(id, term.rows, term.cols)
          .catch((error: unknown) => onNotify(String(error)));
      }
    };
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
      if (!inputGuard.acceptData()) return;
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
      helper?.removeEventListener("input", blockReplayedText, true);
      started = false;
      void forge.closeTerminal(id);
      term.dispose();
      element.replaceChildren();
    };
  }, [instanceID, onNotify, workDir]);

  return (
    <section className="terminal-workspace">
      <div ref={host} className="terminal-host" />
    </section>
  );
}
