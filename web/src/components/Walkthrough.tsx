import { useEffect, useState } from "react";
import {
  type DiffScope,
  forge,
  type Walkthrough as WalkthroughData,
} from "../bridge";
import { DiffView } from "./DiffView";

type Props = {
  scope: DiffScope;
  base: string;
  model: string;
  onOpenFile: (path: string) => void;
  onNotify: (message: string) => void;
};

const scopeLabels: { id: DiffScope; label: string }[] = [
  { id: "all", label: "All changes" },
  { id: "staged", label: "Staged" },
  { id: "worktree", label: "Unstaged" },
  { id: "branch", label: "This branch" },
];

// Walkthrough turns a diff into an ordered tour. The stop list drives which
// file the diff beside it scrolls to, so reading top to bottom walks the
// change in the order it makes sense rather than in path order.
export function Walkthrough({
  scope,
  base,
  model,
  onOpenFile,
  onNotify,
}: Props) {
  const [current, setCurrent] = useState<DiffScope>(scope);
  const [walk, setWalk] = useState<WalkthroughData | null>(null);
  const [diff, setDiff] = useState("");
  const [stop, setStop] = useState(0);
  const [busy, setBusy] = useState(false);
  const [stale, setStale] = useState(false);

  useEffect(() => {
    setWalk(null);
    setStop(0);
    setStale(false);
    void forge
      .gitDiffScope(current, base)
      .then(setDiff)
      .catch((error: unknown) => onNotify(String(error)));
  }, [current, base, onNotify]);

  // The tour describes a diff that may have moved on since; check rather than
  // silently showing an explanation of code that no longer exists.
  useEffect(() => {
    if (!walk) return;
    const check = () =>
      void forge
        .walkthroughStale(current, base, walk.fingerprint)
        .then(setStale)
        .catch(() => {});
    const timer = window.setInterval(check, 5000);
    check();
    return () => window.clearInterval(timer);
  }, [walk, current, base]);

  const generate = async () => {
    setBusy(true);
    try {
      const next = await forge.generateWalkthrough(current, base, model);
      setWalk(next);
      setStop(0);
      setStale(false);
      setDiff(await forge.gitDiffScope(current, base));
    } catch (error) {
      onNotify(String(error));
    } finally {
      setBusy(false);
    }
  };

  const active = walk?.stops[stop];

  return (
    <div className="walk">
      <div className="walk-side">
        <div className="walk-head">
          <select
            value={current}
            onChange={(e) => setCurrent(e.target.value as DiffScope)}
          >
            {scopeLabels.map((s) => (
              <option key={s.id} value={s.id}>
                {s.label}
              </option>
            ))}
          </select>
          <button
            className="btn small primary"
            disabled={busy}
            onClick={() => void generate()}
          >
            {busy ? "Reading…" : walk ? "Regenerate" : "Generate"}
          </button>
        </div>

        {stale ? (
          <div className="walk-stale">
            The code changed since this walkthrough was written.
          </div>
        ) : null}

        {!walk ? (
          <p className="workspace-muted">
            A diff is sorted by path, which is rarely the order the change makes
            sense in. Generate a walkthrough to read it in a useful order.
          </p>
        ) : (
          <>
            <p className="walk-summary">{walk.summary}</p>
            {walk.truncated ? (
              <div className="walk-stale">
                The diff was too large to send in full; later files may be
                uncovered.
              </div>
            ) : null}
            <ol className="walk-stops">
              {walk.stops.map((s, i) => (
                <li key={`${i}-${s.title}`}>
                  <button
                    className={i === stop ? "active" : ""}
                    onClick={() => setStop(i)}
                  >
                    <span className="walk-index">{i + 1}</span>
                    <span className="walk-title">{s.title}</span>
                    {s.tag ? (
                      <span className={`dtag ${s.tag}`}>{s.tag}</span>
                    ) : null}
                  </button>
                </li>
              ))}
            </ol>
            {walk.uncovered.length > 0 ? (
              <div className="walk-uncovered">
                <b>Not covered by any stop</b>
                <ul>
                  {walk.uncovered.map((path) => (
                    <li key={path}>
                      <button onClick={() => onOpenFile(path)}>{path}</button>
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}
          </>
        )}
      </div>

      <div className="walk-main">
        {active ? (
          <div className="walk-explain">
            <h3>
              {stop + 1}. {active.title}
              {active.tag ? (
                <span className={`dtag ${active.tag}`}>{active.tag}</span>
              ) : null}
            </h3>
            <p>{active.explanation}</p>
            <div className="walk-files">
              {active.files.map((path) => (
                <button
                  key={path}
                  className="btn small"
                  onClick={() => onOpenFile(path)}
                >
                  {path}
                </button>
              ))}
            </div>
            <div className="walk-nav">
              <button
                className="btn small"
                disabled={stop === 0}
                onClick={() => setStop(stop - 1)}
              >
                ← Previous
              </button>
              <button
                className="btn small"
                disabled={!walk || stop >= walk.stops.length - 1}
                onClick={() => setStop(stop + 1)}
              >
                Next →
              </button>
            </div>
          </div>
        ) : null}
        <DiffView
          diff={diff}
          focusFile={active?.files[0]}
          onOpenFile={onOpenFile}
          empty="No changes in this scope"
        />
      </div>
    </div>
  );
}
