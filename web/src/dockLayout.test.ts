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

import {
  addTool,
  allTools,
  defaultColumns,
  dropZone,
  findTool,
  moveTool,
  parseColumns,
  removeTool,
  resizeGroups,
  type DockColumns,
} from "./dockLayout";

const terminal = {
  id: "terminal-1",
  kind: "terminal" as const,
  title: "Terminal 1",
};

test("a tab dropped below a group splits that column", () => {
  const columns = defaultColumns();
  const chat = columns.center[0];
  const split = addTool(columns, terminal, {
    side: "center",
    where: "after",
    groupID: chat.id,
  });
  expect(split.center).toHaveLength(2);
  expect(split.center[1].tools[0].id).toBe("terminal-1");
  // The split takes its room from the group it split, not from the column.
  expect(split.center[0].size + split.center[1].size).toBeCloseTo(chat.size);
});

test("a tab dropped on a tab strip stacks and is selected", () => {
  const columns = addTool(defaultColumns(), terminal, {
    side: "left",
    where: "end",
  });
  const moved = moveTool(columns, "terminal-1", {
    side: "center",
    where: "into",
    groupID: columns.center[0].id,
  });
  expect(moved.left).toHaveLength(1);
  expect(moved.center[0].tools.map((tool) => tool.id)).toEqual([
    "chat",
    "terminal-1",
  ]);
  expect(moved.center[0].activeID).toBe("terminal-1");
});

test("emptying a group removes it and hands the tab to a neighbour", () => {
  const columns = defaultColumns();
  const moved = moveTool(columns, "editor", {
    side: "left",
    where: "into",
    groupID: columns.left[0].id,
  });
  expect(moved.right).toHaveLength(0);
  expect(findTool(moved, "editor")?.side).toBe("left");

  const closed = removeTool(moved, "git");
  expect(closed.left[0].tools.map((tool) => tool.id)).toEqual([
    "explorer",
    "editor",
  ]);
});

test("moving the last tab out of a split leaves no dead column space", () => {
  const start = defaultColumns();
  const split = addTool(start, terminal, {
    side: "center",
    where: "after",
    groupID: start.center[0].id,
  });
  const moved = moveTool(split, terminal.id, {
    side: "left",
    where: "into",
    groupID: split.left[0].id,
  });
  expect(moved.center).toHaveLength(1);
  expect(moved.center.reduce((sum, group) => sum + group.size, 0)).toBeCloseTo(
    1,
  );

  const damaged = JSON.stringify({
    ...moved,
    center: moved.center.map((group) => ({ ...group, size: 0.25 })),
  });
  expect(
    parseColumns(damaged).center.reduce((sum, group) => sum + group.size, 0),
  ).toBeCloseTo(1);
});

test("dropping a tab where it already is changes nothing", () => {
  const columns = defaultColumns();
  expect(
    moveTool(columns, "chat", {
      side: "center",
      where: "before",
      groupID: columns.center[0].id,
    }),
  ).toEqual(columns);
});

test("a divider drag splits the space of the two groups it sits between", () => {
  const start = defaultColumns();
  const columns = addTool(start, terminal, {
    side: "center",
    where: "after",
    groupID: start.center[0].id,
  });
  const sized = resizeGroups(columns, "center", 1, 0.75);
  const total = sized.center[0].size + sized.center[1].size;
  expect(sized.center[0].size / total).toBeCloseTo(0.75);
  // A group cannot be dragged shut.
  const squashed = resizeGroups(columns, "center", 1, 0);
  expect(squashed.center[1].size / total).toBeLessThan(0.9);
  expect(squashed.center[0].size).toBeGreaterThan(0);
});

test("a stored layout comes back, junk and gaps and all", () => {
  const start = defaultColumns();
  const columns = moveTool(
    addTool(start, terminal, { side: "center", where: "end" }),
    "git",
    { side: "right", where: "into", groupID: start.right[0].id },
  );
  const restored = parseColumns(JSON.stringify(columns));
  expect(findTool(restored, "terminal-1")?.side).toBe("center");
  expect(
    allTools(restored)
      .map((tool) => tool.id)
      .sort(),
  ).toEqual(
    allTools(columns)
      .map((tool) => tool.id)
      .sort(),
  );

  expect(parseColumns("not json")).toEqual(expect.anything());
  expect(allTools(parseColumns("not json"))).toHaveLength(4);

  // A layout that lost the chat — or duplicated a panel — is repaired, never
  // rendered as a shell with no chat in it.
  const broken = JSON.parse(JSON.stringify(columns)) as DockColumns;
  broken.center = [];
  broken.left[0].tools.push({ ...broken.left[0].tools[0] });
  const repaired = parseColumns(JSON.stringify(broken));
  const ids = allTools(repaired).map((tool) => tool.id);
  expect(ids).toContain("chat");
  expect(new Set(ids).size).toBe(ids.length);
});

test("the drop zone follows the pointer down a group", () => {
  const rect = { top: 0, height: 100 };
  expect(dropZone(5, rect)).toBe("before");
  expect(dropZone(50, rect)).toBe("into");
  expect(dropZone(95, rect)).toBe("after");
});

test("a tab dragged along its own strip reorders it", () => {
  const start = defaultColumns();
  const left = start.left[0].id;
  const columns = addTool(
    addTool(start, terminal, { side: "left", where: "into", groupID: left }),
    { id: "terminal-2", kind: "terminal", title: "Terminal 2" },
    { side: "left", where: "into", groupID: left },
  );
  const group = columns.left[0];
  expect(group.tools.map((tool) => tool.id)).toEqual([
    "explorer",
    "git",
    "terminal-1",
    "terminal-2",
  ]);

  // Dragged to the front.
  const front = moveTool(columns, "terminal-2", {
    side: "left",
    where: "into",
    groupID: group.id,
    index: 0,
  });
  expect(front.left[0].tools.map((tool) => tool.id)).toEqual([
    "terminal-2",
    "explorer",
    "git",
    "terminal-1",
  ]);
  expect(front.left[0].activeID).toBe("terminal-2");

  // Dragged rightwards: the insertion point is read before the tab is lifted,
  // so dropping after "terminal-1" lands after it, not before.
  const back = moveTool(columns, "explorer", {
    side: "left",
    where: "into",
    groupID: group.id,
    index: 3,
  });
  expect(back.left[0].tools.map((tool) => tool.id)).toEqual([
    "git",
    "terminal-1",
    "explorer",
    "terminal-2",
  ]);

  // Dropped where it already is, nothing moves.
  expect(
    moveTool(columns, "git", {
      side: "left",
      where: "into",
      groupID: group.id,
      index: 1,
    }).left[0].tools.map((tool) => tool.id),
  ).toEqual(["explorer", "git", "terminal-1", "terminal-2"]);
});

test("a tab dropped on another strip lands where it was dropped", () => {
  const columns = defaultColumns();
  const moved = moveTool(columns, "editor", {
    side: "left",
    where: "into",
    groupID: columns.left[0].id,
    index: 0,
  });
  expect(moved.left[0].tools.map((tool) => tool.id)).toEqual([
    "editor",
    "explorer",
    "git",
  ]);
});
