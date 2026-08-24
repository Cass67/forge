import { expect, test } from "bun:test";
import {
  closePane,
  isPane,
  paneIDs,
  setRatio,
  splitPane,
  type TerminalNode,
} from "./terminalSplit";

const root: TerminalNode = { id: "a" };

test("splitting puts the new shell beside or below the one split", () => {
  const row = splitPane(root, "a", "row", "b");
  expect(isPane(row)).toBe(false);
  expect(paneIDs(row)).toEqual(["a", "b"]);
  if (!isPane(row)) expect(row.dir).toBe("row");

  // Splitting a pane inside an existing split only touches that pane.
  const nested = splitPane(row, "b", "col", "c");
  expect(paneIDs(nested)).toEqual(["a", "b", "c"]);
  if (!isPane(nested) && !isPane(nested.second)) {
    expect(nested.dir).toBe("row");
    expect(nested.second.dir).toBe("col");
  }
});

test("closing a pane collapses onto its sibling", () => {
  const row = splitPane(root, "a", "row", "b");
  const nested = splitPane(row, "b", "col", "c");
  expect(paneIDs(closePane(nested, "c"))).toEqual(["a", "b"]);
  expect(paneIDs(closePane(closePane(nested, "c"), "b"))).toEqual(["a"]);
  // The last pane stays: an empty terminal panel is not a useful thing.
  const alone = closePane(closePane(closePane(nested, "c"), "b"), "a");
  expect(paneIDs(alone)).toEqual(["a"]);
});

test("a divider cannot squeeze a pane out of existence", () => {
  const row = splitPane(root, "a", "row", "b");
  const wide = setRatio(row, "a", 5);
  const thin = setRatio(row, "a", -5);
  if (!isPane(wide)) expect(wide.ratio).toBe(0.9);
  if (!isPane(thin)) expect(thin.ratio).toBe(0.1);
});

test("splitting an unknown pane changes nothing", () => {
  expect(splitPane(root, "nope", "row", "b")).toEqual(root);
});
