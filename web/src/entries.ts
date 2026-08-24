import type { WireEvent } from "./bridge";

export type Entry =
  | {
      id: number;
      t: "text";
      role: "user" | "agent";
      agent?: string;
      text: string;
      streaming?: boolean;
      images?: string[];
    }
  | { id: number; t: "reasoning"; text: string; streaming?: boolean }
  | {
      id: number;
      t: "tool";
      name: string;
      summary: string;
      output?: string;
      diff?: string;
      isError?: boolean;
      done?: boolean;
    }
  | { id: number; t: "command"; text: string }
  | { id: number; t: "info"; text: string }
  | { id: number; t: "error"; text: string }
  | { id: number; t: "turn"; ok: boolean };

let nextId = 1;
const nid = () => nextId++;

export function userEntry(text: string, images?: string[]): Entry {
  return { id: nid(), t: "text", role: "user", text, images };
}

// closeStreaming marks the trailing streaming entry (agent text or reasoning)
// as complete. Call it before appending a non-streaming entry so the cursor
// stops and the block is finalized.
export function closeStreaming(entries: Entry[]): Entry[] {
  const last = entries[entries.length - 1];
  if (
    last &&
    ((last.t === "text" && last.streaming) ||
      (last.t === "reasoning" && last.streaming))
  ) {
    return [...entries.slice(0, -1), { ...last, streaming: false }];
  }
  return entries;
}

// info appends a runtime note, dropping empty ones: they rendered as bare
// lines with nothing in them.
function info(entries: Entry[], text?: string): Entry[] {
  if (!text || !text.trim()) return entries;
  return [...closeStreaming(entries), { id: nid(), t: "info", text }];
}

// endTurn closes the turn, collapsing repeats. Nothing separates a separator
// from the one before it, so only the first after real content counts.
function endTurn(entries: Entry[], ok: boolean): Entry[] {
  const closed = closeStreaming(entries);
  const last = closed[closed.length - 1];
  if (!last || last.t === "turn") return closed;
  return [...closed, { id: nid(), t: "turn", ok }];
}

export function applyEvent(entries: Entry[], ev: WireEvent): Entry[] {
  switch (ev.kind) {
    case "token": {
      const agent = ev.agent || "forge";
      const last = entries[entries.length - 1];
      if (
        last &&
        last.t === "text" &&
        last.role === "agent" &&
        last.streaming &&
        last.agent === agent
      ) {
        return [
          ...entries.slice(0, -1),
          { ...last, text: last.text + (ev.text || "") },
        ];
      }
      return [
        ...closeStreaming(entries),
        {
          id: nid(),
          t: "text",
          role: "agent",
          agent,
          text: ev.text || "",
          streaming: true,
        },
      ];
    }
    case "reasoning": {
      const last = entries[entries.length - 1];
      if (last && last.t === "reasoning" && last.streaming) {
        return [
          ...entries.slice(0, -1),
          { ...last, text: last.text + (ev.text || "") },
        ];
      }
      return [
        ...closeStreaming(entries),
        { id: nid(), t: "reasoning", text: ev.text || "", streaming: true },
      ];
    }
    case "tool_call": {
      if (ev.agent === "runtime") {
        if (!ev.text || !ev.text.trim()) return entries;
        return [
          ...closeStreaming(entries),
          { id: nid(), t: "command", text: ev.text },
        ];
      }
      return [
        ...closeStreaming(entries),
        {
          id: nid(),
          t: "tool",
          name: ev.agent || "tool",
          summary: ev.text || "",
          done: false,
        },
      ];
    }
    case "tool_result": {
      const out = entries.slice();
      for (let i = out.length - 1; i >= 0; i--) {
        const e = out[i];
        if (e.t === "tool" && e.name === ev.agent && !e.done) {
          out[i] = {
            ...e,
            done: true,
            output: ev.text || "",
            diff: ev.content || undefined,
            isError: ev.is_error,
          };
          return out;
        }
      }
      return out;
    }
    case "error":
      return [
        ...closeStreaming(entries),
        { id: nid(), t: "error", text: ev.error || ev.text || "error" },
      ];

    case "retry":
    case "warning":
      return info(entries, ev.text);
    case "done":
      return endTurn(entries, true);
    case "agent_done":
      // A sub-agent finishing is not a turn boundary. Treating it as one drew
      // a separator per delegated agent, which stacked into a wall of rules
      // whenever a prompt fanned out.
      return closeStreaming(entries);
    case "abort":
      return endTurn(entries, false);
    default:
      return entries;
  }
}
