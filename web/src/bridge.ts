// Wails bridge.
//
// The window has no HTTP server behind it: the frontend calls the bound Go
// service by method name and receives streamed output as application events.
// Frame shapes are unchanged from the earlier WebSocket transport, so the
// reducers and components did not have to move.
import { Call, Events } from "/wails/runtime.js";

const SERVICE = "forge/internal/gui.Service";
const call = <T>(method: string, ...args: unknown[]): Promise<T> =>
  Call.ByName(`${SERVICE}.${method}`, ...args) as Promise<T>;

export type WireEvent = {
  kind: string;
  agent?: string;
  text?: string;
  pass_name?: string;
  pass?: number;
  round?: number;
  is_error?: boolean;
  content?: string;
  duration_ms?: number;
  usage?: {
    input_tokens: number;
    output_tokens: number;
    cached_input_tokens?: number;
    cache_write_tokens?: number;
  };
  context_used?: number;
  context_limit?: number;
  context_estimated?: boolean;
  sub_agent?: string;
  error?: string;
};

export type ClearResult = {
  removed: number;
  failed: number;
  threads: ThreadSummary[];
};

export type ThreadSummary = {
  thread_id: string;
  title: string;
  preview?: string;
  model?: string;
  cwd?: string;
  updated_at: string;
  item_count: number;
};

export type WireAction = {
  tool: string;
  summary: string;
  detail?: string;
  path?: string;
};

export type StoredItem = {
  kind: string;
  at: string;
  message?: { role: string; text?: string; reasoning_content?: string };
  tool_call?: {
    tool_name: string;
    tool_call_id?: string;
    args?: Record<string, unknown>;
  };
  tool_result?: {
    tool_name: string;
    tool_call_id?: string;
    text?: string;
    diff?: string;
    is_error?: boolean;
  };
  turn_complete?: { status?: string };
};

export type Provider = {
  id: string;
  label: string;
  status?: string;
  default_model?: string;
  signed_in: boolean;
  interactive: boolean;
};

export type MCPServer = {
  name: string;
  type: string;
  target?: string;
  enabled: boolean;
  loaded: boolean;
  tools: string[];
};

export type Login = {
  provider: string;
  verify_url: string;
  user_code?: string;
  needs_paste: boolean;
};

export type InitPayload = {
  ready: boolean;
  model: string;
  work_dir: string;
  models: string[];
  providers: Provider[];
  effort?: string;
  efforts?: string[];
  skills: { name: string; description?: string }[];
  thread_id?: string;
  request_mode?: string;
  yolo: boolean;
  notice?: string;
};

export type Workspace = {
  path: string;
  name: string;
  threads: number;
  last_use: string;
  active: boolean;
  missing: boolean;
  pinned: boolean;
};

export type Attachment = {
  id: string;
  path: string;
  name: string;
  mime_type: string;
  size: number;
  width: number;
  height: number;
};

export type RestoreResult = {
  thread_id: string;
  restored: number;
  items: StoredItem[];
};

export type WorkspaceEntry = {
  name: string;
  path: string;
  is_dir: boolean;
  size?: number;
};
export type WorkspaceFile = { path: string; content: string; version: string };
export type GitFileStatus = {
  path: string;
  status: string;
  orig?: string;
  index: string;
  work: string;
  staged: boolean;
  unstaged: boolean;
  untracked: boolean;
  conflict: boolean;
  adds: number;
  dels: number;
};
export type GitStatusResult = {
  repository: boolean;
  branch?: string;
  upstream?: string;
  ahead: number;
  behind: number;
  detached: boolean;
  files: GitFileStatus[];
  root?: string;
  // Names an interrupted operation ("rebase", "merge", …) so the panel can
  // offer continue/abort instead of a bare list of conflicts.
  state?: string;
};
export type GitBranch = {
  name: string;
  current: boolean;
  remote: boolean;
  upstream?: string;
  ahead: number;
  behind: number;
  subject?: string;
  when?: string;
};
export type GitCommit = {
  sha: string;
  short: string;
  author: string;
  when: string;
  subject: string;
  body?: string;
  refs?: string;
};
export type GitStash = { index: number; ref: string; subject: string };
export type GitWorktree = {
  path: string;
  name: string;
  branch?: string;
  head?: string;
  detached: boolean;
  bare: boolean;
  locked: boolean;
  prunable: boolean;
  missing: boolean;
  current: boolean;
  main: boolean;
  ahead: number;
  behind: number;
  dirty: boolean;
};
export type IntegrateResult = {
  merged: boolean;
  into: string;
  from: string;
  conflicts: string[];
  message: string;
};
export type WalkStop = {
  title: string;
  tag?: "key" | "context" | "";
  files: string[];
  explanation: string;
};
export type Walkthrough = {
  scope: string;
  base?: string;
  summary: string;
  stops: WalkStop[];
  uncovered: string[];
  fingerprint: string;
  truncated: boolean;
  model?: string;
  generated_at: string;
};
export type DiffScope = "worktree" | "staged" | "all" | "branch";
export type RunSpec = {
  group: string;
  prompt: string;
  models: string[];
  isolate: boolean;
  base?: string;
  yolo?: boolean;
};
export type RunLaunch = {
  model: string;
  dir: string;
  branch?: string;
  started: boolean;
  error?: string;
  worktree: boolean;
};
export type PreviewInfo = { url: string; target: string };
export type TerminalEvent = { id: string; data?: string; closed?: boolean };

// Wails delivers a payload as event.data, sometimes wrapped in an array.
function payload<T>(event: unknown): T {
  const data = (event as { data?: unknown })?.data;
  return (Array.isArray(data) ? data[0] : data) as T;
}

export const forge = {
  init: () => call<InitPayload>("Init"),
  send: (text: string) => call<void>("Send", text),
  sendWithImages: (text: string, attachments: Attachment[]) =>
    call<void>("SendWithImages", text, attachments),
  approve: (ok: boolean) => call<void>("Approve", ok),
  cancel: () => call<void>("Cancel"),
  newSession: () => call<void>("NewSession"),
  clear: () => call<void>("Clear"),
  switchModel: (model: string) => call<string>("SwitchModel", model),
  models: () => call<string[]>("Models"),
  efforts: (model: string) => call<string[]>("Efforts", model),
  setEffort: (effort: string) => call<void>("SetEffort", effort),
  threads: () => call<ThreadSummary[]>("Threads"),
  history: (threadID: string) => call<StoredItem[]>("History", threadID),
  restore: (threadID: string) => call<RestoreResult>("Restore", threadID),
  deleteThread: (threadID: string) =>
    call<ThreadSummary[]>("DeleteThread", threadID),
  clearThreads: () => call<ClearResult>("ClearThreads"),
  renameThread: (threadID: string, title: string) =>
    call<ThreadSummary[]>("RenameThread", threadID, title),
  workspaces: () => call<Workspace[]>("Workspaces"),
  providers: () => call<Provider[]>("Providers"),
  mcpServers: () => call<MCPServer[]>("MCPServers"),
  signOutProvider: (id: string) => call<Provider[]>("SignOutProvider", id),
  setProviderKey: (id: string, key: string) =>
    call<Provider[]>("SetProviderKey", id, key),
  startProviderLogin: (id: string) => call<Login>("StartProviderLogin", id),
  awaitProviderLogin: (id: string) =>
    call<Provider[]>("AwaitProviderLogin", id),
  completeProviderLogin: (id: string, pasted: string) =>
    call<Provider[]>("CompleteProviderLogin", id, pasted),
  openURL: (url: string) => call<void>("OpenURL", url),
  chooseWorkspace: () => call<string>("ChooseWorkspace"),
  switchWorkspace: (dir: string) => call<void>("SwitchWorkspace", dir),
  pinWorkspace: (dir: string, pinned: boolean) =>
    call<Workspace[]>("PinWorkspace", dir, pinned),
  forgetWorkspace: (dir: string) => call<Workspace[]>("ForgetWorkspace", dir),
  yolo: () => call<boolean>("Yolo"),
  setYolo: (on: boolean) => call<boolean>("SetYolo", on),
  attachImage: (name: string, dataB64: string) =>
    call<Attachment>("AttachImage", name, dataB64),
  attachPath: (path: string) => call<Attachment>("AttachPath", path),
  imagePreview: (path: string) => call<string>("ImagePreview", path),
  startPreview: (target: string) => call<PreviewInfo>("StartPreview", target),
  stopPreview: () => call<void>("StopPreview"),
  listWorkspaceDir: (path: string) =>
    call<WorkspaceEntry[]>("ListWorkspaceDir", path),
  readWorkspaceFile: (path: string) =>
    call<WorkspaceFile>("ReadWorkspaceFile", path),
  writeWorkspaceFile: (path: string, content: string, version: string) =>
    call<WorkspaceFile>("WriteWorkspaceFile", path, content, version),
  gitStatus: () => call<GitStatusResult>("GitStatus"),
  gitDiff: (path: string, staged: boolean) =>
    call<string>("GitDiff", path, staged),
  gitDiffScope: (scope: DiffScope, base: string) =>
    call<string>("GitDiffScope", scope, base),
  gitDefaultBranch: () => call<string>("GitDefaultBranch"),
  gitStage: (paths: string[]) => call<GitStatusResult>("GitStage", paths),
  gitUnstage: (paths: string[]) => call<GitStatusResult>("GitUnstage", paths),
  gitDiscard: (paths: string[]) => call<GitStatusResult>("GitDiscard", paths),
  gitCommit: (message: string, amend: boolean) =>
    call<GitStatusResult>("GitCommit", message, amend),
  gitBranches: (includeRemote: boolean) =>
    call<GitBranch[]>("GitBranches", includeRemote),
  gitCheckout: (name: string) => call<GitStatusResult>("GitCheckout", name),
  gitCreateBranch: (name: string, base: string, checkout: boolean) =>
    call<GitStatusResult>("GitCreateBranch", name, base, checkout),
  gitRenameBranch: (from: string, to: string) =>
    call<GitStatusResult>("GitRenameBranch", from, to),
  gitDeleteBranch: (name: string, force: boolean) =>
    call<GitStatusResult>("GitDeleteBranch", name, force),
  gitFetch: () => call<GitStatusResult>("GitFetch"),
  gitPull: (rebase: boolean) => call<GitStatusResult>("GitPull", rebase),
  gitPush: (force: boolean) => call<GitStatusResult>("GitPush", force),
  gitStash: (message: string, includeUntracked: boolean) =>
    call<GitStatusResult>("GitStash", message, includeUntracked),
  gitStashList: () => call<GitStash[]>("GitStashList"),
  gitStashApply: (index: number, drop: boolean) =>
    call<GitStatusResult>("GitStashApply", index, drop),
  gitStashDrop: (index: number) => call<GitStash[]>("GitStashDrop", index),
  gitLog: (limit: number, skip: number, ref: string) =>
    call<GitCommit[]>("GitLog", limit, skip, ref),
  gitCommitDiff: (sha: string) => call<string>("GitCommitDiff", sha),
  gitResolve: (path: string, side: "ours" | "theirs" | "") =>
    call<GitStatusResult>("GitResolve", path, side),
  gitContinue: (state: string) => call<GitStatusResult>("GitContinue", state),
  gitAbort: (state: string) => call<GitStatusResult>("GitAbort", state),
  gitWorktrees: () => call<GitWorktree[]>("GitWorktrees"),
  gitAddWorktree: (
    branch: string,
    path: string,
    base: string,
    newBranch: boolean,
  ) => call<GitWorktree>("GitAddWorktree", branch, path, base, newBranch),
  gitRemoveWorktree: (path: string, force: boolean, deleteBranch: boolean) =>
    call<GitWorktree[]>("GitRemoveWorktree", path, force, deleteBranch),
  gitIntegrate: (from: string, into: string, squash: boolean) =>
    call<IntegrateResult>("GitIntegrate", from, into, squash),
  generateCommitMessage: (model: string) =>
    call<string>("GenerateCommitMessage", model),
  generateWalkthrough: (scope: DiffScope, base: string, model: string) =>
    call<Walkthrough>("GenerateWalkthrough", scope, base, model),
  walkthroughStale: (scope: DiffScope, base: string, fingerprint: string) =>
    call<boolean>("WalkthroughStale", scope, base, fingerprint),
  startRuns: (spec: RunSpec) => call<RunLaunch[]>("StartRuns", spec),
  startTerminal: (id: string, rows: number, cols: number) =>
    call<void>("StartTerminal", id, rows, cols),
  writeTerminal: (id: string, data: string) =>
    call<void>("WriteTerminal", id, data),
  resizeTerminal: (id: string, rows: number, cols: number) =>
    call<void>("ResizeTerminal", id, rows, cols),
  closeTerminal: (id: string) => call<void>("CloseTerminal", id),

  onEvent: (fn: (ev: WireEvent) => void) =>
    Events.On("forge:event", (e: unknown) => fn(payload<WireEvent>(e))),
  onApproval: (fn: (a: WireAction) => void) =>
    Events.On("forge:approval", (e: unknown) => fn(payload<WireAction>(e))),
  onTurnDone: (fn: () => void) => Events.On("forge:done", () => fn()),
  // Not payload(): that helper unwraps a one-element array, which for a list
  // of filenames yields the first name and leaves the caller iterating its
  // characters. Both wrapping shapes are handled explicitly here.
  onFilesDropped: (fn: (paths: string[]) => void) =>
    Events.On("forge:files", (e: unknown) => {
      const data = (e as { data?: unknown })?.data;
      const raw =
        Array.isArray(data) && Array.isArray(data[0]) ? data[0] : data;
      fn(
        Array.isArray(raw)
          ? raw.filter((v): v is string => typeof v === "string")
          : [],
      );
    }),
  onReady: (fn: () => void) => Events.On("forge:ready", () => fn()),
  onTerminal: (fn: (event: TerminalEvent) => void) =>
    Events.On("forge:terminal", (event: unknown) =>
      fn(payload<TerminalEvent>(event)),
    ),
};
