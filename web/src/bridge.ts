// Wails bridge.
//
// The window has no HTTP server behind it: the frontend calls the bound Go
// service by method name and receives streamed output as application events.
// Frame shapes are unchanged from the earlier WebSocket transport, so the
// reducers and components did not have to move.
import { Call, Events } from "/wails/runtime.js";

const SERVICE = "forge/internal/gui.Service";
const call = <T,>(method: string, ...args: unknown[]): Promise<T> =>
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
  usage?: { input_tokens: number; output_tokens: number };
  context_used?: number;
  context_limit?: number;
  context_estimated?: boolean;
  sub_agent?: string;
  error?: string;
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
  tool_call?: { tool_name: string; tool_call_id?: string; args?: Record<string, unknown> };
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
  deleteThread: (threadID: string) => call<ThreadSummary[]>("DeleteThread", threadID),
  renameThread: (threadID: string, title: string) =>
    call<ThreadSummary[]>("RenameThread", threadID, title),
  workspaces: () => call<Workspace[]>("Workspaces"),
  providers: () => call<Provider[]>("Providers"),
  mcpServers: () => call<MCPServer[]>("MCPServers"),
  signOutProvider: (id: string) => call<Provider[]>("SignOutProvider", id),
  setProviderKey: (id: string, key: string) => call<Provider[]>("SetProviderKey", id, key),
  startProviderLogin: (id: string) => call<Login>("StartProviderLogin", id),
  awaitProviderLogin: (id: string) => call<Provider[]>("AwaitProviderLogin", id),
  completeProviderLogin: (id: string, pasted: string) =>
    call<Provider[]>("CompleteProviderLogin", id, pasted),
  openURL: (url: string) => call<void>("OpenURL", url),
  chooseWorkspace: () => call<string>("ChooseWorkspace"),
  switchWorkspace: (dir: string) => call<void>("SwitchWorkspace", dir),
  pinWorkspace: (dir: string, pinned: boolean) => call<Workspace[]>("PinWorkspace", dir, pinned),
  forgetWorkspace: (dir: string) => call<Workspace[]>("ForgetWorkspace", dir),
  yolo: () => call<boolean>("Yolo"),
  setYolo: (on: boolean) => call<boolean>("SetYolo", on),
  attachImage: (name: string, dataB64: string) => call<Attachment>("AttachImage", name, dataB64),
  attachPath: (path: string) => call<Attachment>("AttachPath", path),

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
      const raw = Array.isArray(data) && Array.isArray(data[0]) ? data[0] : data;
      fn(Array.isArray(raw) ? raw.filter((v): v is string => typeof v === "string") : []);
    }),
  onReady: (fn: () => void) => Events.On("forge:ready", () => fn()),
};
