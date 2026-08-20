// Wire protocol types + WebSocket client for the forge GUI.

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
  tool_call?: { tool_name: string; args?: Record<string, unknown> };
  tool_result?: {
    tool_name: string;
    text?: string;
    diff?: string;
    is_error?: boolean;
  };
  turn_complete?: { status?: string };
};

export type InitFrame = {
  type: "init";
  model: string;
  work_dir: string;
  models: string[];
  providers: { id: string; label: string; status?: string }[];
  effort?: string;
  efforts?: string[];
  skills: { name: string; description?: string }[];
  thread_id?: string;
  request_mode?: string;
};

export type ServerFrame =
  | InitFrame
  | { type: "event"; event: WireEvent }
  | { type: "approval"; action: WireAction }
  | { type: "threads"; items: ThreadSummary[] }
  | {
      type: "history";
      thread_id: string;
      items: StoredItem[];
      restored?: number;
      error?: string;
    }
  | {
      type: "action_result";
      name: string;
      ok: boolean;
      payload?: unknown;
      error?: string;
    }
  | { type: "done" };

export type ClientFrame =
  | { type: "input"; text: string }
  | { type: "approve"; ok: boolean }
  | { type: "action"; name: string; payload?: Record<string, unknown> };

export class WsClient {
  private ws: WebSocket | null = null;
  private queue: ClientFrame[] = [];
  onFrame: (f: ServerFrame) => void = () => {};
  onStatus: (connected: boolean) => void = () => {};

  connect(): void {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const env = (import.meta as unknown as { env?: Record<string, string> }).env;
    const url = env?.VITE_GUI_URL ?? `${proto}://${location.host}/ws`;
    const ws = new WebSocket(url);
    this.ws = ws;
    ws.onopen = () => {
      this.onStatus(true);
      for (const f of this.queue.splice(0)) ws.send(JSON.stringify(f));
    };
    ws.onclose = () => this.onStatus(false);
    ws.onerror = () => this.onStatus(false);
    ws.onmessage = (m) => {
      try {
        this.onFrame(JSON.parse(String(m.data)) as ServerFrame);
      } catch {
        // ignore malformed frames
      }
    };
  }

  send(f: ClientFrame): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(f));
    } else {
      this.queue.push(f);
    }
  }
}
