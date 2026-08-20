import { useState } from "react";
import type { Entry } from "../entries";
import { DiffView } from "./DiffView";

type ToolEntry = Extract<Entry, { t: "tool" }>;

const MAX_PREVIEW = 4000;

export function ToolCard({ entry }: { entry: ToolEntry }) {
  const [open, setOpen] = useState(false);
  const [full, setFull] = useState(false);
  const status = entry.isError ? "err" : entry.done ? "ok" : "run";
  const out = entry.output ?? "";
  const truncated = !full && out.length > MAX_PREVIEW;

  return (
    <div className={`toolcard ${status} ${open ? "open" : ""}`}>
      <button className="tc-head" onClick={() => setOpen((v) => !v)}>
        <span className={`tc-dot ${status}`} />
        <span className="tc-name">{entry.name}</span>
        <span className="tc-summary">{entry.summary}</span>
        {entry.diff ? <span className="tc-tag">diff</span> : null}
        <span className="tc-caret">{open ? "▾" : "▸"}</span>
      </button>
      {open && (
        <div className="tc-body">
          {!entry.done ? <div className="tc-pending">running…</div> : null}
          {entry.diff ? <DiffView diff={entry.diff} /> : null}
          {out ? (
            <>
              <pre className="tc-out">{truncated ? out.slice(0, MAX_PREVIEW) : out}</pre>
              {truncated ? (
                <button className="btn" onClick={() => setFull(true)}>
                  show all {out.length.toLocaleString()} chars
                </button>
              ) : null}
            </>
          ) : null}
        </div>
      )}
    </div>
  );
}
