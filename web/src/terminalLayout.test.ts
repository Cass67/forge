import { expect, test } from "bun:test";
import {
  closeTerminalPane,
  resizeTerminalSplit,
  splitTerminal,
  terminalIDs,
  type TerminalLayout,
} from "./terminalLayout";

const leaf = (id: string): TerminalLayout => ({ kind: "terminal", id });

test("terminal layouts split recursively and collapse when a pane closes", () => {
  const split = splitTerminal(leaf("one"), "one", "horizontal", "two");
  const nested = splitTerminal(split, "two", "vertical", "three");
  expect(terminalIDs(nested)).toEqual(["one", "two", "three"]);

  const closed = closeTerminalPane(nested, "two");
  expect(closed).not.toBeNull();
  expect(terminalIDs(closed!)).toEqual(["one", "three"]);
});

test("terminal split ratios stay usable", () => {
  const split = splitTerminal(leaf("one"), "one", "horizontal", "two");
  if (split.kind !== "split") throw new Error("expected split");
  expect(resizeTerminalSplit(split, split.id, 0.01)).toMatchObject({
    ratio: 0.15,
  });
  expect(resizeTerminalSplit(split, split.id, 0.99)).toMatchObject({
    ratio: 0.85,
  });
});
