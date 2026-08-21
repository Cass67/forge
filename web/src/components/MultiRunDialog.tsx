import { useState } from "react";
import { forge, type RunLaunch } from "../bridge";

type Props = {
  models: string[];
  currentModel: string;
  isRepo: boolean;
  onClose: () => void;
  onNotify: (message: string) => void;
};

const MAX_RUNS = 5;

// Multi-run sends one prompt to several models at once. Forge binds one chat
// runtime to one window, so each run opens its own window — isolated in its
// own worktree unless the user turns that off.
export function MultiRunDialog({
  models,
  currentModel,
  isRepo,
  onClose,
  onNotify,
}: Props) {
  const [group, setGroup] = useState("");
  const [prompt, setPrompt] = useState("");
  const [picked, setPicked] = useState<string[]>(
    currentModel ? [currentModel] : [],
  );
  const [isolate, setIsolate] = useState(isRepo);
  const [busy, setBusy] = useState(false);
  const [launched, setLaunched] = useState<RunLaunch[] | null>(null);

  const toggle = (model: string) =>
    setPicked((current) =>
      current.includes(model)
        ? current.filter((m) => m !== model)
        : current.length >= MAX_RUNS
          ? current
          : [...current, model],
    );

  const start = async () => {
    setBusy(true);
    try {
      setLaunched(
        await forge.startRuns({
          group,
          prompt,
          models: picked,
          isolate: isolate && isRepo,
        }),
      );
    } catch (error) {
      onNotify(String(error));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal multirun" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <span className="modal-badge">multi-run</span>
          <button className="icon-btn close" onClick={onClose}>
            ✕
          </button>
        </div>

        {launched ? (
          <div className="multirun-result">
            <ul>
              {launched.map((run) => (
                <li key={run.model} className={run.started ? "" : "failed"}>
                  <b>{run.model}</b>
                  {run.branch ? <code>{run.branch}</code> : null}
                  <span>{run.started ? "window opened" : run.error}</span>
                </li>
              ))}
            </ul>
            <p className="workspace-muted">
              Each run is an ordinary session. Keep the one you want and delete
              the rest — removing a run's worktree is in Source Control →
              Worktrees.
            </p>
            <button className="btn primary" onClick={onClose}>
              Done
            </button>
          </div>
        ) : (
          <>
            <label className="multirun-field">
              <span>Group name</span>
              <input
                value={group}
                placeholder="optional — names the branches"
                onChange={(e) => setGroup(e.target.value)}
              />
            </label>
            <label className="multirun-field">
              <span>Prompt</span>
              <textarea
                rows={5}
                value={prompt}
                placeholder="The task every run gets"
                onChange={(e) => setPrompt(e.target.value)}
              />
            </label>
            <div className="multirun-field">
              <span>
                Models ({picked.length}/{MAX_RUNS})
              </span>
              <div className="multirun-models">
                {models.map((model) => (
                  <button
                    key={model}
                    className={picked.includes(model) ? "active" : ""}
                    onClick={() => toggle(model)}
                  >
                    {model}
                  </button>
                ))}
              </div>
            </div>
            <label className="git-amend">
              <input
                type="checkbox"
                checked={isolate && isRepo}
                disabled={!isRepo}
                onChange={(e) => setIsolate(e.target.checked)}
              />
              give each run its own worktree and branch
              {isRepo ? "" : " (needs a git repository)"}
            </label>
            <div className="modal-actions">
              <button
                className="btn primary"
                disabled={busy || picked.length === 0 || !prompt.trim()}
                onClick={() => void start()}
              >
                {busy ? "Starting…" : `Start ${picked.length} run(s)`}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
