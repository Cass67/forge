import type { WireEvent } from "./ws";

export type Entry =
  | { id: number; t: "text"; role: "user" | "agent"; agent?: string; text: string; streaming?: boolean }
  | { id: number; t: "reasoning"; text: string; streaming?: boolean }
  | { id: number; t: "tool"; name: string; summary: string; output?: string; diff?: string; isError?: boolean; done?: boolean }
  | { id: number; t: "info"; text: string }
  | { id: number; t: "error"; text: string }
  | { id: number; t: "turn"; ok: boolean };

let nextId = 1;
const nid = () => nextId++;

export function userEntry(text: string): Entry {
  return { id: nid(), t: "text", role: "user", text };
}

export function applyEvent(entries: Entry[], ev: WireEvent): Entry[] {
  switch (ev.kind) {
    case "token": {
      const agent = ev.agent || "forge";
      const last = entries[entries.length - 1];
      if (last && last.t === "text" && last.role === "agent" && last.streaming && last.agent === agent) {
        return [...entries.slice(0, -1), { ...last, text: last.text + (ev.text || "") }];
      }
      return [...closeStreaming(entries), { id: nid(), t: "text", role: "agent", agent, text: ev.text || "", streaming: true }];
    }
    case "reasoning": {
      const last = entries[entries.length - 1];
      if (last && last.t === "reasoning" && last.streaming) {
        return [...entries.slice(0, -1), { ...last, text: last.text + (ev.text || "") }];
      }
      return [...closeStreaming(entries), { id: nid(), t: "reasoning", text: ev.text || "", streaming: true }];
    }
    case "tool_call": {
      if (ev.agent === "runtime") {
        return [...closeStreaming(entries), { id: nid(), t: "info", text: ev.text || "" }];
      }
      return [...closeStreaming(entries), { id: nid(), t: "tool", name: ev.agent || "tool", summary: ev.text || "", done: false }];
    }
    case "tool_result": {
      const out = entries.slice();
      for (let i = out.length - 1; i >= 0; i--) {
        const e = out[i];
        if (e.t === "tool" && e.name === ev.agent && !e.done) {
          out[i] = { ...e, done: true, output: ev.text || "", diff: ev.content || undefined, isError: ev.is_error };
          return out;
        }
      }
      return out;
    }
    case "error":
      return [...closeStreaming(entries), { id: nid(), t: "error", text: ev.error || ev.text || "error" }];
    case "retry":
    case "warning":
      return [...closeStreaming(entries), { id: nid(), t: "info", text: ev.text || "" }];
    case "done":
    case "agent_done":
      return [...closeStreaming(entries), { id: nid(), t: "turn", ok: true }];
    case "abort":
      return [...closeStreaming(entries), { id: nid(), t: "turn", ok: false }];
    default:
      return entries;
  }
}
