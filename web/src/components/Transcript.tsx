import { useEffect, useLayoutEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import type { Entry } from "../entries";
import { ToolCard } from "./ToolCard";
import type { Prefs } from "./SettingsPanel";

function CopyButton({ text, label = "copy" }: { text: string; label?: string }) {
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
      <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeHighlight]}>
        {text}
      </ReactMarkdown>
      {streaming ? <span className="cursor" /> : null}
    </div>
  );
}

export function Transcript({ entries, prefs, busy }: { entries: Entry[]; prefs: Prefs; busy: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  // Follow the tail only while the user is already at the bottom, so scrolling
  // back to read something isn't yanked away by streaming tokens.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const onScroll = () => {
      stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
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
      {entries.map((e) => {
        switch (e.t) {
          case "text":
            return e.role === "user" ? (
              <div key={e.id} className="msg user">
                <div className="body">{e.text}</div>
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
              <details key={e.id} className="reasoning" open={e.streaming || prefs.expandReasoning}>
                <summary>
                  <span className="think-dot" />
                  {e.streaming ? "thinking…" : "thought process"}
                </summary>
                <div className="reasoning-body">
                  {e.text}
                  {e.streaming ? <span className="cursor" /> : null}
                </div>
              </details>
            );
          case "tool":
            if (!prefs.showTools) return null;
            return <ToolCard key={e.id} entry={e} defaultOpen={prefs.expandTools} />;
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
            return <div key={e.id} className={`turn-sep ${e.ok ? "ok" : "bad"}`} />;
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
    </div>
  );
}
