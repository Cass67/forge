import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import hljs from "highlight.js/lib/common";
import { forge } from "../bridge";
import type { GitTab } from "../gitTabs";
import { Walkthrough } from "./Walkthrough";
import {
  changeAnchors,
  type DiffFile,
  type DiffHunk,
  type DiffRow,
  diffStat,
  parseDiff,
  splitRows,
} from "../diff";

// A run of unchanged lines longer than this is folded away: the reviewer asked
// to see a change, not the file around it.
const FOLD_THRESHOLD = 12;
const FOLD_KEEP = 4;

type Props = {
  diff: string;
  // Actions the source-control panel supplies; omitted for read-only diffs
  // such as a commit's changes.
  onStageFile?: (path: string) => void;
  onUnstageFile?: (path: string) => void;
  onOpenFile?: (path: string) => void;
  // Scrolls the named file into view when it changes, which is how the
  // walkthrough drives the viewer.
  focusFile?: string;
  empty?: string;
};

type Fold = { kind: "fold"; count: number; id: string };
type Block = { kind: "rows"; rows: DiffRow[] } | Fold;

const languageByExtension: Record<string, string> = {
  ts: "typescript",
  tsx: "typescript",
  js: "javascript",
  jsx: "javascript",
  go: "go",
  py: "python",
  rs: "rust",
  rb: "ruby",
  java: "java",
  c: "c",
  h: "c",
  cc: "cpp",
  cpp: "cpp",
  hpp: "cpp",
  cs: "csharp",
  sh: "bash",
  bash: "bash",
  zsh: "bash",
  json: "json",
  yml: "yaml",
  yaml: "yaml",
  toml: "ini",
  ini: "ini",
  md: "markdown",
  css: "css",
  scss: "scss",
  html: "xml",
  xml: "xml",
  sql: "sql",
};

function languageFor(path: string): string | null {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  const name = languageByExtension[ext];
  if (!name) return null;
  return hljs.getLanguage(name) ? name : null;
}

// Rows are highlighted one line at a time. A construct spanning several lines
// — a block comment, a template literal — therefore highlights imperfectly;
// paying for whole-file context would mean re-highlighting both sides of every
// hunk on every render, which a diff view does not earn.
// Highlighting is cached across renders: a diff view re-renders on every
// fold, mode switch and scroll target, and re-tokenising thousands of lines
// each time is the difference between instant and visibly slow.
const highlightCache = new Map<string, string | null>();
const HIGHLIGHT_CACHE_MAX = 20_000;

function highlight(text: string, language: string | null): string | null {
  if (!language || text === "") return null;
  const key = `${language}\u0000${text}`;
  const cached = highlightCache.get(key);
  if (cached !== undefined) return cached;
  let value: string | null = null;
  try {
    value = hljs.highlight(text, { language, ignoreIllegals: true }).value;
  } catch {
    value = null;
  }
  if (highlightCache.size >= HIGHLIGHT_CACHE_MAX) highlightCache.clear();
  highlightCache.set(key, value);
  return value;
}

// RowText renders one line. Word-level spans win over syntax colour when a
// line was replaced: which characters changed is the more useful signal, and
// splicing ranges into highlighted markup is not worth the complexity.
function RowText({ row, language }: { row: DiffRow; language: string | null }) {
  if (row.spans && row.spans.length > 0) {
    const [start, end] = row.spans[0];
    return (
      <span className="dt">
        {row.text.slice(0, start)}
        <mark className="dword">{row.text.slice(start, end)}</mark>
        {row.text.slice(end)}
      </span>
    );
  }
  const html = highlight(row.text, language);
  if (html === null) return <span className="dt">{row.text || " "}</span>;
  return <span className="dt" dangerouslySetInnerHTML={{ __html: html }} />;
}

// blocksFor folds long runs of context into a single expander.
function blocksFor(
  hunk: DiffHunk,
  hunkID: string,
  opened: Set<string>,
): Block[] {
  const blocks: Block[] = [];
  let buffer: DiffRow[] = [];
  const flush = () => {
    if (buffer.length) blocks.push({ kind: "rows", rows: buffer });
    buffer = [];
  };
  let i = 0;
  while (i < hunk.rows.length) {
    if (hunk.rows[i].kind !== "ctx") {
      buffer.push(hunk.rows[i]);
      i++;
      continue;
    }
    let j = i;
    while (j < hunk.rows.length && hunk.rows[j].kind === "ctx") j++;
    const run = hunk.rows.slice(i, j);
    const id = `${hunkID}-fold-${i}`;
    if (run.length <= FOLD_THRESHOLD || opened.has(id)) {
      buffer.push(...run);
    } else {
      // Keep a few lines either side so the fold still reads as context.
      const head = i === 0 ? [] : run.slice(0, FOLD_KEEP);
      const tail = j === hunk.rows.length ? [] : run.slice(-FOLD_KEEP);
      buffer.push(...head);
      flush();
      blocks.push({
        kind: "fold",
        count: run.length - head.length - tail.length,
        id,
      });
      buffer.push(...tail);
    }
    i = j;
  }
  flush();
  return blocks;
}

function Gutter({ row }: { row: DiffRow | null }) {
  return (
    <>
      <span className="dn">{row?.old ?? ""}</span>
      <span className="dn">{row?.neu ?? ""}</span>
    </>
  );
}

function sign(kind: DiffRow["kind"]): string {
  return kind === "add" ? "+" : kind === "del" ? "−" : " ";
}

export function DiffView({
  diff,
  onStageFile,
  onUnstageFile,
  onOpenFile,
  focusFile,
  empty = "No changes",
}: Props) {
  const files = useMemo(() => parseDiff(diff), [diff]);
  const anchors = useMemo(() => changeAnchors(files), [files]);
  const stat = useMemo(() => diffStat(files), [files]);
  const [split, setSplit] = useState(false);
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [opened, setOpened] = useState<Set<string>>(new Set());
  const [cursor, setCursor] = useState(0);
  const scroller = useRef<HTMLDivElement>(null);

  // A new diff invalidates every anchor and every fold decision.
  useEffect(() => {
    setCursor(0);
    setOpened(new Set());
    setCollapsed(new Set());
  }, [diff]);

  const goto = useCallback(
    (index: number) => {
      if (anchors.length === 0) return;
      const next = (index + anchors.length) % anchors.length;
      setCursor(next);
      const target = scroller.current?.querySelector(
        `[data-anchor="${anchors[next]}"]`,
      );
      target?.scrollIntoView({ block: "center", behavior: "smooth" });
    },
    [anchors],
  );

  useEffect(() => {
    if (!focusFile) return;
    const target = scroller.current?.querySelector(
      `[data-file="${CSS.escape(focusFile)}"]`,
    );
    target?.scrollIntoView({ block: "start", behavior: "smooth" });
  }, [focusFile, diff]);

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.metaKey || event.ctrlKey || event.altKey) return;
    if (event.key === "n" || event.key === "j") {
      event.preventDefault();
      goto(cursor + 1);
    } else if (event.key === "p" || event.key === "k") {
      event.preventDefault();
      goto(cursor - 1);
    } else if (event.key === "s") {
      event.preventDefault();
      setSplit((v) => !v);
    }
  };

  const toggle = (set: Set<string>, id: string) => {
    const next = new Set(set);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    return next;
  };

  if (files.length === 0) {
    return <div className="diffwrap empty-diff">{empty}</div>;
  }

  return (
    <div className="diffwrap" tabIndex={0} onKeyDown={onKeyDown}>
      <div className="diffbar">
        <span className="diffstat">
          <span className="d-add">+{stat.adds}</span>
          <span className="d-del">−{stat.dels}</span>
          <span className="d-files">
            {files.length} {files.length === 1 ? "file" : "files"}
          </span>
        </span>
        <span className="diffbar-actions">
          <button
            className="btn small"
            onClick={() => goto(cursor - 1)}
            disabled={anchors.length === 0}
            title="Previous change (p)"
          >
            ↑
          </button>
          <span className="diff-cursor">
            {anchors.length === 0 ? "0/0" : `${cursor + 1}/${anchors.length}`}
          </span>
          <button
            className="btn small"
            onClick={() => goto(cursor + 1)}
            disabled={anchors.length === 0}
            title="Next change (n)"
          >
            ↓
          </button>
          <button
            className={`btn small ${split ? "on" : ""}`}
            onClick={() => setSplit((v) => !v)}
            title="Side-by-side (s)"
          >
            {split ? "Split" : "Unified"}
          </button>
        </span>
      </div>

      <div className="diffscroll" ref={scroller}>
        {files.map((file, fi) => {
          const id = file.path || `file-${fi}`;
          const language = languageFor(file.path);
          const isCollapsed = collapsed.has(id);
          return (
            <section className="difffile" key={id} data-file={file.path}>
              <header className="difffile-head">
                <button
                  className="difffile-toggle"
                  onClick={() => setCollapsed((c) => toggle(c, id))}
                  aria-expanded={!isCollapsed}
                >
                  {isCollapsed ? "▸" : "▾"}
                </button>
                <button
                  className="difffile-name"
                  onClick={() => onOpenFile?.(file.path)}
                  title={file.path}
                  disabled={!onOpenFile}
                >
                  {file.renamed && file.oldPath ? `${file.oldPath} → ` : ""}
                  {file.path}
                </button>
                {file.added ? <span className="dtag">new</span> : null}
                {file.deleted ? (
                  <span className="dtag warn">deleted</span>
                ) : null}
                {file.binary ? <span className="dtag">binary</span> : null}
                <span className="difffile-stat">
                  <span className="d-add">+{file.adds}</span>
                  <span className="d-del">−{file.dels}</span>
                </span>
                {onStageFile ? (
                  <button
                    className="btn small"
                    onClick={() => onStageFile(file.path)}
                    title="Stage this file"
                  >
                    Stage
                  </button>
                ) : null}
                {onUnstageFile ? (
                  <button
                    className="btn small"
                    onClick={() => onUnstageFile(file.path)}
                    title="Unstage this file"
                  >
                    Unstage
                  </button>
                ) : null}
              </header>

              {isCollapsed ? null : file.binary ? (
                <div className="diffbox binary">Binary file not shown</div>
              ) : (
                <div className={`diffbox ${split ? "split" : ""}`}>
                  {file.hunks.map((hunk, hi) => {
                    const hunkID = `${fi}-${hi}`;
                    return (
                      <div className="diffhunk" key={hunkID}>
                        <div className="dl hunk">{hunk.header}</div>
                        {blocksFor(hunk, hunkID, opened).map((block, bi) =>
                          block.kind === "fold" ? (
                            <button
                              className="dl fold"
                              key={`${hunkID}-${bi}`}
                              onClick={() =>
                                setOpened((o) => toggle(o, block.id))
                              }
                            >
                              ⋯ {block.count} unchanged lines
                            </button>
                          ) : split ? (
                            splitRows(block.rows).map((pair, ri) => (
                              <div
                                className="dsplit"
                                key={`${hunkID}-${bi}-${ri}`}
                              >
                                <div
                                  className={`dl ${pair.left ? pair.left.kind : "pad"}`}
                                  data-anchor={pair.left?.anchor}
                                >
                                  <span className="dn">
                                    {pair.left?.old ?? ""}
                                  </span>
                                  <span className="dg">
                                    {pair.left ? sign(pair.left.kind) : " "}
                                  </span>
                                  {pair.left ? (
                                    <RowText
                                      row={pair.left}
                                      language={language}
                                    />
                                  ) : (
                                    <span className="dt" />
                                  )}
                                </div>
                                <div
                                  className={`dl ${pair.right ? pair.right.kind : "pad"}`}
                                  data-anchor={pair.right?.anchor}
                                >
                                  <span className="dn">
                                    {pair.right?.neu ?? ""}
                                  </span>
                                  <span className="dg">
                                    {pair.right ? sign(pair.right.kind) : " "}
                                  </span>
                                  {pair.right ? (
                                    <RowText
                                      row={pair.right}
                                      language={language}
                                    />
                                  ) : (
                                    <span className="dt" />
                                  )}
                                </div>
                              </div>
                            ))
                          ) : (
                            block.rows.map((row, ri) => (
                              <div
                                className={`dl ${row.kind}`}
                                key={`${hunkID}-${bi}-${ri}`}
                                data-anchor={row.anchor}
                              >
                                <Gutter row={row} />
                                <span className="dg">{sign(row.kind)}</span>
                                <RowText row={row} language={language} />
                              </div>
                            ))
                          ),
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </section>
          );
        })}
      </div>
    </div>
  );
}

// GitTabView resolves one source-control tab to its content: a file's diff, a
// commit's diff, a whole scope, or the walkthrough over one.
export function GitTabView({
  tab,
  model,
  onOpenFile,
  onStage,
  onUnstage,
  onNotify,
  revision,
}: {
  tab: GitTab;
  model: string;
  onOpenFile: (path: string) => void;
  onStage: (path: string) => void;
  onUnstage: (path: string) => void;
  onNotify: (message: string) => void;
  // Bumped by the panel whenever the index or working tree moves, so an open
  // diff re-reads instead of showing what the file looked like on open.
  revision: number;
}) {
  const [diff, setDiff] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    if (tab.kind === "walkthrough") return;
    const load =
      tab.kind === "file"
        ? forge.gitDiff(tab.path, tab.staged)
        : tab.kind === "commit"
          ? forge.gitCommitDiff(tab.sha)
          : forge.gitDiffScope(tab.scope, tab.base);
    let live = true;
    void load
      .then((text) => {
        if (!live) return;
        setDiff(text);
        setError("");
      })
      .catch((err: unknown) => {
        if (live) setError(String(err));
      });
    return () => {
      live = false;
    };
  }, [tab, revision]);

  if (tab.kind === "walkthrough") {
    return (
      <Walkthrough
        scope={tab.scope}
        base={tab.base}
        model={model}
        onOpenFile={onOpenFile}
        onNotify={onNotify}
      />
    );
  }
  if (error) return <div className="diffwrap empty-diff">{error}</div>;
  return (
    <DiffView
      diff={diff}
      onOpenFile={onOpenFile}
      onStageFile={tab.kind === "file" && !tab.staged ? onStage : undefined}
      onUnstageFile={tab.kind === "file" && tab.staged ? onUnstage : undefined}
      empty={
        tab.kind === "commit" ? "This commit changed nothing" : "No changes"
      }
    />
  );
}
