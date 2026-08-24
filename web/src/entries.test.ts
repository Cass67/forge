import { expect, test } from "bun:test";
import { applyEvent, type Entry } from "./entries";
import type { WireEvent } from "./bridge";

const ev = (e: Partial<WireEvent> & { kind: string }): WireEvent =>
  e as WireEvent;
const run = (events: WireEvent[]): Entry[] =>
  events.reduce(applyEvent, [] as Entry[]);

test("a fan-out of sub-agents does not stack turn separators", () => {
  const entries = run([
    ev({ kind: "token", text: "working" }),
    ev({ kind: "agent_done" }),
    ev({ kind: "agent_done" }),
    ev({ kind: "agent_done" }),
    ev({ kind: "done" }),
  ]);
  expect(entries.filter((e) => e.t === "turn")).toHaveLength(1);
});

test("repeated done events collapse to one separator", () => {
  const entries = run([
    ev({ kind: "token", text: "hi" }),
    ev({ kind: "done" }),
    ev({ kind: "done" }),
    ev({ kind: "done" }),
  ]);
  expect(entries.filter((e) => e.t === "turn")).toHaveLength(1);
});

test("a separator needs content before it", () => {
  expect(run([ev({ kind: "done" })])).toHaveLength(0);
});

test("runtime command output gets a command block and empty output is dropped", () => {
  const entries = run([
    ev({ kind: "tool_call", agent: "runtime", text: "" }),
    ev({ kind: "tool_call", agent: "runtime", text: "   " }),
    ev({ kind: "warning", text: "" }),
    ev({ kind: "tool_call", agent: "runtime", text: "reloaded plugins" }),
  ]);
  expect(entries).toHaveLength(1);
  expect(entries[0]).toMatchObject({ t: "command", text: "reloaded plugins" });
});

test("reasoning deltas accumulate into one streaming block", () => {
  const entries = run([
    ev({ kind: "reasoning", text: "think" }),
    ev({ kind: "reasoning", text: "ing..." }),
  ]);
  expect(entries).toHaveLength(1);
  expect(entries[0]).toMatchObject({
    t: "reasoning",
    text: "thinking...",
    streaming: true,
  });
});
