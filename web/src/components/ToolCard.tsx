import { useMemo, useState } from "react";
import type { Entry } from "../entries";
import { highlightBlock, languageForPath, parseGutter } from "../toolOutput";
import { DiffView } from "./DiffView";
import { ImagePreview } from "./ImagePreview";

// A tool that names an image file is worth showing rather than describing.
const IMAGE_RE = /(\/?[^\s"']+\.(?:png|jpe?g|gif))/i;

function imagePathIn(entry: ToolEntry): string {
  const m = IMAGE_RE.exec(entry.summary || "");
  return m ? m[1] : "";
}

type ToolEntry = Extract<Entry, { t: "tool" }>;

const MAX_PREVIEW = 4000;

// File reads come back with a "  120 | " gutter. Split it off so the code can be
// highlighted as one block and the numbers ride in their own column.
function ToolOutput({ text, path }: { text: string; path: string }) {
  const view = useMemo(() => {
    const parsed = parseGutter(text);
    if (!parsed) return null;
    const html = highlightBlock(parsed.code, languageForPath(path));
    return { ...parsed, html };
  }, [text, path]);

  if (!view) return <pre className="tc-out">{text}</pre>;

  return (
    <div className="tc-code">
      {view.header ? <div className="tc-code-header">{view.header}</div> : null}
      <div className="tc-code-rows">
        <pre className="tc-gutter" aria-hidden="true">
          {view.numbers.map((n) => (n ? n : "")).join("\n")}
        </pre>
        {view.html ? (
          <pre
            className="tc-out hljs"
            dangerouslySetInnerHTML={{ __html: view.html }}
          />
        ) : (
          <pre className="tc-out">{view.code}</pre>
        )}
      </div>
    </div>
  );
}

export function ToolCard({
  entry,
  defaultOpen = false,
}: {
  entry: ToolEntry;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
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
          {imagePathIn(entry) ? (
            <ImagePreview path={imagePathIn(entry)} alt={entry.name} />
          ) : null}
          {entry.diff ? <DiffView diff={entry.diff} /> : null}
          {out ? (
            <>
              <ToolOutput
                text={truncated ? out.slice(0, MAX_PREVIEW) : out}
                path={entry.summary || ""}
              />
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
