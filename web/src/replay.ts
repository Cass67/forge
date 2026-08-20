import type { StoredItem } from "./bridge";
import { closeStreaming, type Entry } from "./entries";

let rid = 10_000_000;
const nid = () => rid++;

// summarizeArgs renders a tool's args into a short one-line summary for the
// collapsed tool card. Falls back to a JSON dump for unknown shapes.
function summarizeArgs(args?: Record<string, unknown>): string {
  if (!args) return "";
  const common = ["path", "file", "command", "pattern", "query", "url", "text", "message"];
  for (const k of common) {
    const v = args[k];
    if (typeof v === "string" && v) return v.length > 120 ? v.slice(0, 117) + "…" : v;
  }
  try {
    const s = JSON.stringify(args);
    return s.length > 120 ? s.slice(0, 117) + "…" : s;
  } catch {
    return "";
  }
}

// itemsToEntries converts stored thread items (from a history/restore frame)
// into the same Entry model the live event stream produces, so a restored
// thread renders identically to a live one.
export function itemsToEntries(items: StoredItem[]): Entry[] {
  const out: Entry[] = [];
  const openTools = new Map<string, number>(); // tool_call_id -> index in out

  for (const it of items) {
    switch (it.kind) {
      case "user_message": {
        const text = it.message?.text ?? "";
        if (text) out.push({ id: nid(), t: "text", role: "user", text });
        break;
      }
      case "assistant_message": {
        const rc = it.message?.reasoning_content ?? "";
        if (rc) out.push({ id: nid(), t: "reasoning", text: rc });
        const text = it.message?.text ?? "";
        if (text) out.push({ id: nid(), t: "text", role: "agent", text });
        break;
      }
      case "tool_call": {
        const tc = it.tool_call;
        const idx = out.length;
        out.push({ id: nid(), t: "tool", name: tc?.tool_name ?? "tool", summary: summarizeArgs(tc?.args), done: false });
        if (tc?.tool_call_id) openTools.set(tc.tool_call_id, idx);
        break;
      }
      case "tool_result": {
        const tr = it.tool_result;
        let idx = tr?.tool_call_id ? openTools.get(tr.tool_call_id) : undefined;
        if (idx === undefined) {
          for (let i = out.length - 1; i >= 0; i--) {
            const e = out[i];
            if (e.t === "tool" && e.name === (tr?.tool_name ?? "") && !e.done) {
              idx = i;
              break;
            }
          }
        }
        const done = { done: true, output: tr?.text ?? "", diff: tr?.diff || undefined, isError: tr?.is_error };
        if (idx !== undefined) {
          const e = out[idx];
          if (e.t === "tool") out[idx] = { ...e, ...done };
        } else {
          out.push({ id: nid(), t: "tool", name: tr?.tool_name ?? "tool", summary: "", ...done });
        }
        if (tr?.tool_call_id) openTools.delete(tr.tool_call_id);
        break;
      }
      case "turn_complete": {
        const status = it.turn_complete?.status ?? "completed";
        out.push({ id: nid(), t: "turn", ok: status !== "failed" && status !== "interrupted" });
        break;
      }
      default:
        break;
    }
  }
  return closeStreaming(out);
}
