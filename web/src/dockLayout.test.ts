import { expect, test } from "bun:test";
import {
  clampDock,
  DEFAULT_DOCK_WIDTHS,
  dockFraction,
  parseDockWidths,
} from "./dockLayout";

test("a dock cannot be dragged shut or over the chat column", () => {
  expect(clampDock(DEFAULT_DOCK_WIDTHS, "left", 0).left).toBeGreaterThan(0.05);
  const wide = clampDock(DEFAULT_DOCK_WIDTHS, "right", 0.95);
  expect(wide.right + wide.left).toBeLessThanOrEqual(0.8);
});

test("the other dock's width limits this one", () => {
  const fat = { left: 0.5, right: 0.1 };
  expect(clampDock(fat, "right", 0.5).right).toBeCloseTo(0.3);
});

test("dragging right widens the left dock and narrows the right one", () => {
  const rect = { left: 100, width: 1000 };
  expect(dockFraction("left", 400, rect)).toBeCloseTo(0.3);
  expect(dockFraction("right", 400, rect)).toBeCloseTo(0.7);
});

test("stored widths are kept, and junk falls back to the defaults", () => {
  expect(parseDockWidths(JSON.stringify({ left: 0.25, right: 0.25 }))).toEqual({
    left: 0.25,
    right: 0.25,
  });
  expect(parseDockWidths("not json")).toEqual(DEFAULT_DOCK_WIDTHS);
  expect(parseDockWidths(null)).toEqual(DEFAULT_DOCK_WIDTHS);

  const clamped = parseDockWidths(JSON.stringify({ left: 9, right: 9 }));
  expect(clamped.left + clamped.right).toBeLessThanOrEqual(0.8);
});
