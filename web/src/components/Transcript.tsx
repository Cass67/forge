import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import type { Entry } from "../entries";
import { ToolCard } from "./ToolCard";
import { ImagePreview } from "./ImagePreview";
import type { Prefs } from "./SettingsPanel";
import { deriveTimelineRows } from "../timeline";

const TIMELINE_PAGE = 100;

function CopyButton({
  text,
  label = "copy",
}: {
  text: string;
  label?: string;
}) {
  const [done, setDone] = useState(false);
  return (
    <button
      className="icon-btn copy"
      onClick={() => {
        void navigator.clipboard.writeText(text).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1200);
        });
      }}
    >
      {done ? "copied" : label}
    </button>
  );
}

function AgentText({ text, streaming }: { text: string; streaming?: boolean }) {
  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeHighlight]}
      >
        {text}
      </ReactMarkdown>
      {streaming ? <span className="cursor" /> : null}
    </div>
  );
}

export function Transcript({
  entries,
  prefs,
  busy,
}: {
  entries: Entry[];
  prefs: Prefs;
  busy: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [historyReading, setHistoryReading] = useState(false);
  const [visibleCount, setVisibleCount] = useState(TIMELINE_PAGE);
  const timeline = useMemo(
    () => deriveTimelineRows(entries, visibleCount),
    [entries, visibleCount],
  );

  // Follow the tail only while the user is already at the bottom, so scrolling
  // back to read something isn't yanked away by streaming tokens.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const onScroll = () => {
      stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
      setHistoryReading(!stick.current);
    };
    el.addEventListener("scroll", onScroll);
    return () => el.removeEventListener("scroll", onScroll);
  }, []);

  useLayoutEffect(() => {
    const el = ref.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [entries]);

  if (entries.length === 0) {
    return (
      <div className="transcript empty" ref={ref}>
        <div className="splash">
          <div className="splash-mark">forge</div>
          <div className="splash-hint">
            ask for a change, or type <code>/</code> for commands
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="transcript" ref={ref}>
      {timeline.hasEarlier ? (
        <button
          className="timeline-control load-earlier"
          onClick={() => setVisibleCount((count) => count + TIMELINE_PAGE)}
        >
          Load earlier
        </button>
      ) : null}
      {timeline.rows.map(({ entry: e }) => {
        switch (e.t) {
          case "text":
            return e.role === "user" ? (
              <div key={e.id} className="msg user">
                {e.text ? <div className="body">{e.text}</div> : null}
                {e.images && e.images.length > 0 ? (
                  <div className="msg-images">
                    {e.images.map((p) => (
                      <ImagePreview key={p} path={p} />
                    ))}
                  </div>
                ) : null}
              </div>
            ) : (
              <div key={e.id} className="msg agent">
                <div className="who">
                  <span>{e.agent || "forge"}</span>
                  {!e.streaming ? <CopyButton text={e.text} /> : null}
                </div>
                <AgentText text={e.text} streaming={e.streaming} />
              </div>
            );
          case "reasoning":
            if (!prefs.showReasoning) return null;
            return (
              <details
                key={e.id}
                className="reasoning"
                open={e.streaming || prefs.expandReasoning}
              >
                <summary>
                  <span className="think-dot" />
                  {e.streaming ? "thinking…" : "thought process"}
                </summary>
                <div className="reasoning-body">
                  <AgentText text={e.text} streaming={e.streaming} />
                </div>
              </details>
            );
          case "tool":
            if (!prefs.showTools) return null;
            return (
              <ToolCard key={e.id} entry={e} defaultOpen={prefs.expandTools} />
            );
          case "command":
            return (
              <div key={e.id} className="command-output">
                <div className="command-output-head">
                  <span>output</span>
                  <CopyButton text={e.text} />
                </div>
                <pre>{e.text}</pre>
              </div>
            );
          case "info":
            return (
              <div key={e.id} className="info-line">
                {e.text}
              </div>
            );
          case "error":
            return (
              <div key={e.id} className="error-line">
                {e.text}
              </div>
            );
          case "turn":
            return (
              <div key={e.id} className={`turn-sep ${e.ok ? "ok" : "bad"}`} />
            );
          default:
            return null;
        }
      })}
      {busy ? (
        <div className="working">
          <span className="dot" />
          <span className="dot" />
          <span className="dot" />
        </div>
      ) : null}
      {historyReading ? (
        <button
          className="timeline-control jump-latest"
          onClick={() => {
            const el = ref.current;
            if (el) el.scrollTop = el.scrollHeight;
            stick.current = true;
            setHistoryReading(false);
          }}
        >
          Jump to latest
        </button>
      ) : null}
    </div>
  );
}
