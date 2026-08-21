import { expect, test } from "bun:test";
import {
  closeTab,
  emptyTabs,
  nextAfterClose,
  openTab,
  pruneTabs,
  setStatus,
} from "./sessionTabs";

test("opening is idempotent and keeps order", () => {
  let tabs = openTab(emptyTabs, "a");
  tabs = openTab(tabs, "b");
  tabs = openTab(tabs, "a");
  expect(tabs.open).toEqual(["a", "b"]);
});

test("closing a tab drops its remembered status", () => {
  let tabs = setStatus(openTab(openTab(emptyTabs, "a"), "b"), "a", "failed");
  tabs = closeTab(tabs, "a");
  expect(tabs.open).toEqual(["b"]);
  expect(tabs.status.a).toBeUndefined();
});

test("closing focuses the tab that took its place", () => {
  const tabs = openTab(openTab(openTab(emptyTabs, "a"), "b"), "c");
  expect(nextAfterClose(tabs, "b")).toBe("c");
  // Closing the last tab falls back to the new last one.
  expect(nextAfterClose(tabs, "c")).toBe("b");
  expect(nextAfterClose(emptyTabs, "a")).toBe("");
});

test("pruning removes tabs whose threads are gone", () => {
  const tabs = setStatus(openTab(openTab(emptyTabs, "a"), "b"), "b", "done");
  const pruned = pruneTabs(tabs, ["a"]);
  expect(pruned.open).toEqual(["a"]);
  expect(pruned.status.b).toBeUndefined();
});

test("pruning nothing returns the same object so React can skip the render", () => {
  const tabs = openTab(emptyTabs, "a");
  expect(pruneTabs(tabs, ["a", "b"])).toBe(tabs);
});
