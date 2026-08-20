import type { StoredItem } from "./ws";
import { closeStreaming, type Entry } from "./entries";

let rid = 10_000_000;
const nid = () => rid++;

export function itemsToEntries(items: StoredItem[]): Entry[] {
  const out: Entry[] = [];
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
