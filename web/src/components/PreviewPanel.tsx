import { useCallback, useEffect, useRef, useState } from "react";
import { forge } from "../bridge";
import {
  formatPick,
  loadTarget,
  type PreviewLog,
  type PreviewPick,
  pushLog,
  saveTarget,
} from "../previewContext";

type Props = {
  workDir: string;
  onNotify: (message: string) => void;
};

const CHANNEL = "forge-preview";

// The pane frames the app through forge's own proxy, which injects a bridge
// script into the page. Everything below is that conversation: the pane asks
// for a pick or a capture, the bridge answers with what the user clicked, a
// picture of the screen and whatever the console has been saying.
export function PreviewPanel({ workDir, onNotify }: Props) {
  const [target, setTarget] = useState(() => loadTarget(workDir));
  const [proxyURL, setProxyURL] = useState("");
  const [picking, setPicking] = useState(false);
  const [pick, setPick] = useState<PreviewPick | null>(null);
  const [note, setNote] = useState("");
  const [logs, setLogs] = useState<PreviewLog[]>([]);
  const [showLogs, setShowLogs] = useState(false);
  const [sending, setSending] = useState(false);
  const frame = useRef<HTMLIFrameElement>(null);

  useEffect(() => {
    setTarget(loadTarget(workDir));
    setProxyURL("");
    setPick(null);
    setLogs([]);
  }, [workDir]);

  const toBridge = useCallback((type: string, data: object = {}) => {
    frame.current?.contentWindow?.postMessage(
      { channel: CHANNEL, type, ...data },
      "*",
    );
  }, []);

  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      // Only the framed page may drive this pane.
      if (event.source !== frame.current?.contentWindow) return;
      const data = event.data as {
        channel?: string;
        type?: string;
        payload?: unknown;
      };
      if (data?.channel !== CHANNEL) return;
      if (data.type === "log") {
        setLogs((current) => pushLog(current, data.payload as PreviewLog));
        return;
      }
      if (data.type === "picking") {
        setPicking((data.payload as { on: boolean }).on);
        return;
      }
      if (data.type === "picked") {
        setPick(data.payload as PreviewPick);
        setPicking(false);
        return;
      }
      if (data.type === "captured") {
        const shot = data.payload as PreviewPick;
        if (!shot.screenshot) {
          onNotify(shot.screenshotError || "the page could not be rasterised");
          return;
        }
        void forge
          .attachImage("preview.png", shot.screenshot)
          .then((attachment) =>
            forge.sendWithImages(`Screenshot of ${shot.url}`, [attachment]),
          )
          .then(() => onNotify("screenshot sent to chat"))
          .catch((error: unknown) => onNotify(String(error)));
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [onNotify]);

  useEffect(() => () => void forge.stopPreview().catch(() => {}), []);

  const open = () => {
    saveTarget(workDir, target);
    setLogs([]);
    setPick(null);
    void forge
      .startPreview(target)
      .then((info) => setProxyURL(info.url))
      .catch((error: unknown) => onNotify(String(error)));
  };

  const send = () => {
    if (!pick) return;
    setSending(true);
    const text = formatPick(pick, logs, note);
    const deliver = pick.screenshot
      ? forge
          .attachImage("preview.png", pick.screenshot)
          .then((attachment) => forge.sendWithImages(text, [attachment]))
      : forge.send(text);
    void deliver
      .then(() => {
        setPick(null);
        setNote("");
        onNotify("sent the picked element to chat");
      })
      .catch((error: unknown) => onNotify(String(error)))
      .finally(() => setSending(false));
  };

  const errors = logs.filter((log) => log.level === "error").length;

  return (
    <section className="preview-panel">
      <div className="preview-bar">
        <input
          aria-label="Preview address"
          onChange={(event) => setTarget(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") open();
          }}
          placeholder="http://localhost:5173"
          value={target}
        />
        <button onClick={open} title="Load this address in the preview">
          {proxyURL ? "Reload" : "Open"}
        </button>
        <button
          className={picking ? "active" : ""}
          disabled={!proxyURL}
          onClick={() => toBridge("pick", { on: !picking })}
          title="Click an element in the preview to describe it to the agent"
        >
          ⌖ Pick
        </button>
        <button
          disabled={!proxyURL}
          onClick={() => toBridge("capture")}
          title="Send a screenshot of the preview to chat"
        >
          Shot
        </button>
        <button
          className={errors ? "preview-errors" : ""}
          disabled={logs.length === 0}
          onClick={() => setShowLogs((current) => !current)}
          title="Console output from the previewed app"
        >
          {errors ? `${errors} ●` : `${logs.length} ◦`}
        </button>
      </div>

      {proxyURL ? (
        <iframe
          className="preview-frame"
          key={proxyURL}
          ref={frame}
          src={proxyURL}
          title="App preview"
        />
      ) : (
        <div className="workspace-empty">
          Start your dev server, then open its address here
        </div>
      )}

      {showLogs && logs.length > 0 ? (
        <ul className="preview-logs">
          {logs
            .slice()
            .reverse()
            .map((log, index) => (
              <li className={log.level} key={`${log.at}-${index}`}>
                <b>{log.level}</b> {log.text}
              </li>
            ))}
        </ul>
      ) : null}

      {pick ? (
        <div className="preview-pick">
          <div className="preview-pick-head">
            <code>{pick.selector}</code>
            <button
              aria-label="Discard the picked element"
              onClick={() => setPick(null)}
            >
              ×
            </button>
          </div>
          {pick.screenshot ? (
            <img alt="Preview screenshot" src={pick.screenshot} />
          ) : (
            <div className="workspace-muted">
              No screenshot: {pick.screenshotError}
            </div>
          )}
          <textarea
            aria-label="What should change?"
            onChange={(event) => setNote(event.target.value)}
            placeholder="What should change about this?"
            value={note}
          />
          <button disabled={sending} onClick={send}>
            Send to chat
          </button>
        </div>
      ) : null}
    </section>
  );
}
