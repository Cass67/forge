import { useCallback, useEffect, useMemo, useState } from "react";
import {
  forge,
  type GitBranch,
  type GitCommit,
  type GitFileStatus,
  type GitStash,
  type GitStatusResult,
  type GitWorktree,
} from "../bridge";
import {
  commitTab,
  fileTab,
  type GitTab,
  scopeTab,
  walkthroughTab,
} from "../gitTabs";

type Props = {
  status: GitStatusResult | null;
  onStatus: (status: GitStatusResult) => void;
  onRefresh: () => void;
  onOpenTab: (tab: GitTab) => void;
  onOpenFile: (path: string) => void;
  onNotify: (message: string) => void;
  onMultiRun: () => void;
  model: string;
};

type Section = "changes" | "history" | "branches" | "worktrees";

const sections: { id: Section; label: string }[] = [
  { id: "changes", label: "Changes" },
  { id: "history", label: "History" },
  { id: "branches", label: "Branches" },
  { id: "worktrees", label: "Worktrees" },
];

function shortStatus(file: GitFileStatus): string {
  if (file.conflict) return "!";
  if (file.untracked) return "U";
  const code = file.staged ? file.index : file.work;
  return code === "." ? "M" : code;
}

function FileRow({
  file,
  onOpen,
  onStage,
  onUnstage,
  onDiscard,
}: {
  file: GitFileStatus;
  onOpen: () => void;
  onStage?: () => void;
  onUnstage?: () => void;
  onDiscard?: () => void;
}) {
  const name = file.path.split("/").pop() ?? file.path;
  const dir = file.path
    .slice(0, file.path.length - name.length)
    .replace(/\/$/, "");
  return (
    <li className={`git-row ${file.conflict ? "conflict" : ""}`}>
      <button className="git-row-open" onClick={onOpen} title={file.path}>
        <span className={`git-code s-${shortStatus(file)}`}>
          {shortStatus(file)}
        </span>
        <span className="git-name">{name}</span>
        {dir ? <span className="git-dir">{dir}</span> : null}
        {file.adds || file.dels ? (
          <span className="git-stat">
            <span className="d-add">+{file.adds}</span>
            <span className="d-del">−{file.dels}</span>
          </span>
        ) : null}
      </button>
      <span className="git-row-actions">
        {onDiscard ? (
          <button onClick={onDiscard} title="Discard changes to this file">
            ↺
          </button>
        ) : null}
        {onStage ? (
          <button onClick={onStage} title="Stage this file">
            +
          </button>
        ) : null}
        {onUnstage ? (
          <button onClick={onUnstage} title="Unstage this file">
            −
          </button>
        ) : null}
      </span>
    </li>
  );
}

export function GitPanel({
  status,
  onStatus,
  onRefresh,
  onOpenTab,
  onOpenFile,
  onNotify,
  onMultiRun,
  model,
}: Props) {
  const [section, setSection] = useState<Section>("changes");
  const [message, setMessage] = useState("");
  const [amend, setAmend] = useState(false);
  const [busy, setBusy] = useState("");
  const [base, setBase] = useState("");

  useEffect(() => {
    void forge
      .gitDefaultBranch()
      .then(setBase)
      .catch(() => setBase(""));
  }, [status?.root]);

  // run wraps every git call: they all return the new status, and every one of
  // them can fail in a way the user needs to read.
  const run = useCallback(
    async (label: string, fn: () => Promise<GitStatusResult>) => {
      setBusy(label);
      try {
        onStatus(await fn());
      } catch (error) {
        onNotify(String(error));
      } finally {
        setBusy("");
      }
    },
    [onNotify, onStatus],
  );

  const { staged, unstaged, conflicts } = useMemo(() => {
    const files = status?.files ?? [];
    return {
      staged: files.filter((f) => f.staged && !f.conflict),
      unstaged: files.filter((f) => f.unstaged && !f.conflict),
      conflicts: files.filter((f) => f.conflict),
    };
  }, [status]);

  const generate = async () => {
    setBusy("message");
    try {
      setMessage(await forge.generateCommitMessage(model));
    } catch (error) {
      onNotify(String(error));
    } finally {
      setBusy("");
    }
  };

  if (!status?.repository) {
    return <div className="workspace-muted">Not a Git repository</div>;
  }

  return (
    <div className="git-panel">
      <div className="git-head">
        <button
          className="git-branch"
          onClick={() => setSection("branches")}
          title={
            status.upstream ? `tracking ${status.upstream}` : "no upstream"
          }
        >
          <span aria-hidden="true">⑂</span>
          {status.detached ? "detached" : status.branch || "—"}
          {status.behind ? (
            <span className="git-ab">↓{status.behind}</span>
          ) : null}
          {status.ahead ? (
            <span className="git-ab">↑{status.ahead}</span>
          ) : null}
        </button>
        <span className="git-head-actions">
          <button
            onClick={() => void run("fetch", forge.gitFetch)}
            title="Fetch"
          >
            ⟳
          </button>
          <button
            onClick={() => void run("pull", () => forge.gitPull(true))}
            title="Pull (rebase)"
          >
            ↓
          </button>
          <button
            onClick={() => void run("push", () => forge.gitPush(false))}
            title="Push"
          >
            ↑
          </button>
          <button onClick={onRefresh} title="Refresh">
            ↻
          </button>
        </span>
      </div>

      {status.state ? (
        <div className="git-banner">
          <span>{status.state} in progress</span>
          <button
            className="btn small"
            onClick={() =>
              void run("continue", () => forge.gitContinue(status.state!))
            }
          >
            Continue
          </button>
          <button
            className="btn small"
            onClick={() =>
              void run("abort", () => forge.gitAbort(status.state!))
            }
          >
            Abort
          </button>
        </div>
      ) : null}

      <nav className="git-sections">
        {sections.map((s) => (
          <button
            key={s.id}
            className={section === s.id ? "active" : ""}
            onClick={() => setSection(s.id)}
          >
            {s.label}
          </button>
        ))}
      </nav>

      {section === "changes" ? (
        <div className="git-body">
          <div className="git-commitbox">
            <textarea
              value={message}
              placeholder="Commit message"
              onChange={(e) => setMessage(e.target.value)}
              rows={3}
            />
            <div className="git-commit-actions">
              <button
                className="btn small"
                onClick={() => void generate()}
                disabled={busy !== ""}
                title="Draft a message from the staged diff"
              >
                {busy === "message" ? "…" : "✨ Draft"}
              </button>
              <label className="git-amend">
                <input
                  type="checkbox"
                  checked={amend}
                  onChange={(e) => setAmend(e.target.checked)}
                />
                amend
              </label>
              <button
                className="btn small primary"
                disabled={busy !== "" || (staged.length === 0 && !amend)}
                onClick={() =>
                  void run("commit", async () => {
                    const next = await forge.gitCommit(message, amend);
                    setMessage("");
                    setAmend(false);
                    return next;
                  })
                }
              >
                Commit
              </button>
            </div>
          </div>

          <div className="git-review">
            <button
              className="btn small"
              onClick={() => onOpenTab(scopeTab("all", base))}
            >
              Review all changes
            </button>
            <button
              className="btn small"
              onClick={() => onOpenTab(walkthroughTab("all", base))}
              title="Group the diff into an explained tour"
            >
              Walkthrough
            </button>
          </div>

          {conflicts.length > 0 ? (
            <Group title={`Conflicts (${conflicts.length})`}>
              {conflicts.map((file) => (
                <li className="git-row conflict" key={file.path}>
                  <button
                    className="git-row-open"
                    onClick={() => onOpenFile(file.path)}
                    title={file.path}
                  >
                    <span className="git-code s-!">!</span>
                    <span className="git-name">{file.path}</span>
                  </button>
                  <span className="git-row-actions">
                    <button
                      onClick={() =>
                        void run("ours", () =>
                          forge.gitResolve(file.path, "ours"),
                        )
                      }
                      title="Keep our side"
                    >
                      ours
                    </button>
                    <button
                      onClick={() =>
                        void run("theirs", () =>
                          forge.gitResolve(file.path, "theirs"),
                        )
                      }
                      title="Keep their side"
                    >
                      theirs
                    </button>
                    <button
                      onClick={() =>
                        void run("resolved", () =>
                          forge.gitResolve(file.path, ""),
                        )
                      }
                      title="Mark resolved as edited"
                    >
                      ✓
                    </button>
                  </span>
                </li>
              ))}
            </Group>
          ) : null}

          <Group
            title={`Staged (${staged.length})`}
            action={
              staged.length > 0
                ? {
                    label: "Unstage all",
                    run: () =>
                      void run("unstage", () =>
                        forge.gitUnstage(staged.map((f) => f.path)),
                      ),
                  }
                : undefined
            }
          >
            {staged.map((file) => (
              <FileRow
                key={file.path}
                file={file}
                onOpen={() => onOpenTab(fileTab(file.path, true))}
                onUnstage={() =>
                  void run("unstage", () => forge.gitUnstage([file.path]))
                }
              />
            ))}
          </Group>

          <Group
            title={`Changes (${unstaged.length})`}
            action={
              unstaged.length > 0
                ? {
                    label: "Stage all",
                    run: () =>
                      void run("stage", () =>
                        forge.gitStage(unstaged.map((f) => f.path)),
                      ),
                  }
                : undefined
            }
          >
            {unstaged.map((file) => (
              <FileRow
                key={file.path}
                file={file}
                onOpen={() => onOpenTab(fileTab(file.path, false))}
                onStage={() =>
                  void run("stage", () => forge.gitStage([file.path]))
                }
                onDiscard={() => {
                  const what = file.untracked
                    ? `Delete untracked file ${file.path}?`
                    : `Discard changes to ${file.path}?`;
                  if (window.confirm(what))
                    void run("discard", () => forge.gitDiscard([file.path]));
                }}
              />
            ))}
          </Group>

          <StashSection run={run} onNotify={onNotify} status={status} />
        </div>
      ) : null}

      {section === "history" ? (
        <History
          onOpenTab={onOpenTab}
          onNotify={onNotify}
          branch={status.branch}
        />
      ) : null}

      {section === "branches" ? (
        <Branches status={status} run={run} onNotify={onNotify} base={base} />
      ) : null}

      {section === "worktrees" ? (
        <Worktrees
          onNotify={onNotify}
          onRefresh={onRefresh}
          base={base}
          onMultiRun={onMultiRun}
        />
      ) : null}
    </div>
  );
}

function Group({
  title,
  action,
  children,
}: {
  title: string;
  action?: { label: string; run: () => void };
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(true);
  const empty = Array.isArray(children) && children.length === 0;
  return (
    <section className="git-group">
      <header>
        <button className="git-group-toggle" onClick={() => setOpen((v) => !v)}>
          {open ? "▾" : "▸"} {title}
        </button>
        {action ? (
          <button className="btn small" onClick={action.run}>
            {action.label}
          </button>
        ) : null}
      </header>
      {open ? (
        empty ? (
          <div className="workspace-muted">nothing here</div>
        ) : (
          <ul className="git-list">{children}</ul>
        )
      ) : null}
    </section>
  );
}

type Runner = (
  label: string,
  fn: () => Promise<GitStatusResult>,
) => Promise<void>;

function StashSection({
  run,
  onNotify,
  status,
}: {
  run: Runner;
  onNotify: (message: string) => void;
  status: GitStatusResult;
}) {
  const [stashes, setStashes] = useState<GitStash[]>([]);
  const reload = useCallback(() => {
    void forge
      .gitStashList()
      .then(setStashes)
      .catch((error: unknown) => onNotify(String(error)));
  }, [onNotify]);

  useEffect(reload, [reload, status]);

  return (
    <section className="git-group">
      <header>
        <span className="git-group-toggle">Stashes ({stashes.length})</span>
        <button
          className="btn small"
          disabled={status.files.length === 0}
          onClick={() => void run("stash", () => forge.gitStash("", true))}
        >
          Stash all
        </button>
      </header>
      {stashes.length === 0 ? null : (
        <ul className="git-list">
          {stashes.map((stash) => (
            <li className="git-row" key={stash.ref}>
              <span className="git-row-open" title={stash.subject}>
                <span className="git-name">{stash.subject || stash.ref}</span>
              </span>
              <span className="git-row-actions">
                <button
                  onClick={() =>
                    void run("stash", () =>
                      forge.gitStashApply(stash.index, true),
                    )
                  }
                  title="Pop this stash"
                >
                  ↥
                </button>
                <button
                  onClick={() => {
                    if (!window.confirm(`Drop ${stash.ref}?`)) return;
                    void forge
                      .gitStashDrop(stash.index)
                      .then(setStashes)
                      .catch((error: unknown) => onNotify(String(error)));
                  }}
                  title="Drop this stash"
                >
                  ✕
                </button>
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function History({
  onOpenTab,
  onNotify,
  branch,
}: {
  onOpenTab: (tab: GitTab) => void;
  onNotify: (message: string) => void;
  branch?: string;
}) {
  const [commits, setCommits] = useState<GitCommit[]>([]);
  const [done, setDone] = useState(false);

  const load = useCallback(
    (skip: number) => {
      void forge
        .gitLog(50, skip, "")
        .then((page) => {
          setDone(page.length < 50);
          setCommits((current) => (skip === 0 ? page : [...current, ...page]));
        })
        .catch((error: unknown) => onNotify(String(error)));
    },
    [onNotify],
  );

  useEffect(() => {
    setCommits([]);
    setDone(false);
    load(0);
  }, [load, branch]);

  return (
    <div className="git-body">
      <ul className="git-list">
        {commits.map((commit) => (
          <li className="git-commit" key={commit.sha}>
            <button
              onClick={() => onOpenTab(commitTab(commit.sha, commit.subject))}
              title={commit.body || commit.subject}
            >
              <span className="git-commit-subject">{commit.subject}</span>
              <span className="git-commit-meta">
                <code>{commit.short}</code> {commit.author} · {commit.when}
              </span>
              {commit.refs ? (
                <span className="git-refs">{commit.refs}</span>
              ) : null}
            </button>
          </li>
        ))}
      </ul>
      {done || commits.length === 0 ? null : (
        <button className="btn small" onClick={() => load(commits.length)}>
          Load more
        </button>
      )}
    </div>
  );
}

function Branches({
  status,
  run,
  onNotify,
  base,
}: {
  status: GitStatusResult;
  run: Runner;
  onNotify: (message: string) => void;
  base: string;
}) {
  const [branches, setBranches] = useState<GitBranch[]>([]);
  const [remote, setRemote] = useState(false);
  const [creating, setCreating] = useState("");

  const reload = useCallback(() => {
    void forge
      .gitBranches(remote)
      .then(setBranches)
      .catch((error: unknown) => onNotify(String(error)));
  }, [onNotify, remote]);

  useEffect(reload, [reload, status.branch]);

  return (
    <div className="git-body">
      <div className="git-newbranch">
        <input
          value={creating}
          placeholder={`New branch from ${base || "HEAD"}`}
          onChange={(e) => setCreating(e.target.value)}
          onKeyDown={(e) => {
            if (e.key !== "Enter" || !creating.trim()) return;
            void run("branch", async () => {
              const next = await forge.gitCreateBranch(
                creating.trim(),
                base,
                true,
              );
              setCreating("");
              return next;
            });
          }}
        />
        <label className="git-amend">
          <input
            type="checkbox"
            checked={remote}
            onChange={(e) => setRemote(e.target.checked)}
          />
          remotes
        </label>
      </div>
      <ul className="git-list">
        {branches.map((branch) => (
          <li
            className={`git-row ${branch.current ? "active" : ""}`}
            key={`${branch.remote}-${branch.name}`}
          >
            <button
              className="git-row-open"
              onClick={() => {
                if (branch.current) return;
                void run("checkout", () => forge.gitCheckout(branch.name));
              }}
              title={branch.subject}
            >
              <span className="git-name">{branch.name}</span>
              {branch.ahead ? (
                <span className="git-ab">↑{branch.ahead}</span>
              ) : null}
              {branch.behind ? (
                <span className="git-ab">↓{branch.behind}</span>
              ) : null}
              <span className="git-dir">{branch.when}</span>
            </button>
            {branch.current || branch.remote ? null : (
              <span className="git-row-actions">
                <button
                  onClick={() => {
                    const to = window.prompt("Rename branch to", branch.name);
                    if (to && to !== branch.name)
                      void run("rename", () =>
                        forge.gitRenameBranch(branch.name, to),
                      );
                  }}
                  title="Rename"
                >
                  ✎
                </button>
                <button
                  onClick={() => {
                    if (!window.confirm(`Delete branch ${branch.name}?`))
                      return;
                    void run("delete", () =>
                      forge.gitDeleteBranch(branch.name, false),
                    );
                  }}
                  title="Delete"
                >
                  ✕
                </button>
              </span>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function Worktrees({
  onNotify,
  onRefresh,
  base,
  onMultiRun,
}: {
  onNotify: (message: string) => void;
  onRefresh: () => void;
  base: string;
  onMultiRun: () => void;
}) {
  const [trees, setTrees] = useState<GitWorktree[]>([]);
  const [branch, setBranch] = useState("");
  const [busy, setBusy] = useState(false);

  const reload = useCallback(() => {
    void forge
      .gitWorktrees()
      .then(setTrees)
      .catch((error: unknown) => onNotify(String(error)));
  }, [onNotify]);

  useEffect(reload, [reload]);

  const create = async () => {
    const name = branch.trim();
    if (!name) return;
    setBusy(true);
    try {
      const tree = await forge.gitAddWorktree(name, "", base, true);
      setBranch("");
      reload();
      onNotify(`worktree ready at ${tree.path}`);
    } catch (error) {
      onNotify(String(error));
    } finally {
      setBusy(false);
    }
  };

  const integrate = async (tree: GitWorktree) => {
    if (!tree.branch) return;
    const into = window.prompt(`Merge ${tree.branch} into`, base || "main");
    if (!into) return;
    try {
      const result = await forge.gitIntegrate(tree.branch, into, false);
      onRefresh();
      if (result.merged) onNotify(result.message);
      else
        onNotify(
          `merge stopped with conflicts in ${result.conflicts.length} file(s) — resolve them in Changes`,
        );
    } catch (error) {
      onNotify(String(error));
    }
  };

  return (
    <div className="git-body">
      <div className="git-newbranch">
        <input
          value={branch}
          placeholder={`New worktree branch from ${base || "HEAD"}`}
          onChange={(e) => setBranch(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") void create();
          }}
        />
        <button
          className="btn small"
          disabled={busy || !branch.trim()}
          onClick={() => void create()}
        >
          Add
        </button>
      </div>
      <button className="btn small" onClick={onMultiRun}>
        Multi-run…
      </button>
      <ul className="git-list">
        {trees.map((tree) => (
          <li
            className={`git-row ${tree.current ? "active" : ""}`}
            key={tree.path}
          >
            <button
              className="git-row-open"
              onClick={() => forge.switchWorkspace(tree.path)}
              disabled={tree.current || tree.missing}
              title={tree.path}
            >
              <span className="git-name">{tree.branch || tree.name}</span>
              {tree.main ? <span className="dtag">main</span> : null}
              {tree.dirty ? <span className="dtag">dirty</span> : null}
              {tree.missing ? <span className="dtag warn">missing</span> : null}
              {tree.ahead ? (
                <span className="git-ab">↑{tree.ahead}</span>
              ) : null}
              {tree.behind ? (
                <span className="git-ab">↓{tree.behind}</span>
              ) : null}
            </button>
            <span className="git-row-actions">
              {tree.main || !tree.branch ? null : (
                <button
                  onClick={() => void integrate(tree)}
                  title="Merge this branch back"
                >
                  ⤵
                </button>
              )}
              {tree.main ? null : (
                <button
                  onClick={() => {
                    if (
                      !window.confirm(
                        `Remove worktree ${tree.path}? The branch is kept.`,
                      )
                    )
                      return;
                    void forge
                      .gitRemoveWorktree(tree.path, false, false)
                      .then(setTrees)
                      .catch((error: unknown) => onNotify(String(error)));
                  }}
                  title="Remove this worktree"
                >
                  ✕
                </button>
              )}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
