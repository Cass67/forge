import { expect, test } from "bun:test";
import {
  allTools,
  nextTerminalNumber,
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
  columnLayout,
  defaultColumns,
  dropZone,
  findTool,
  moveTool,
  parseColumns,
  removeTool,
  resizeGroups,
  showsDivider,
  SIDES,
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

test("a terminal tab reaches every column and every drop mode", () => {
  const start = addTool(defaultColumns(), terminal, {
    side: "left",
    where: "end",
  });
  const where = (columns: DockColumns) => findTool(columns, terminal.id)?.side;

  // Into another column's group, stacking as a tab.
  const stacked = moveTool(start, terminal.id, {
    side: "right",
    where: "into",
    groupID: start.right[0].id,
  });
  expect(where(stacked)).toBe("right");
  expect(findTool(stacked, terminal.id)?.group.tools.length).toBeGreaterThan(1);

  // Above and below a group, splitting that column.
  for (const side of SIDES) {
    for (const edge of ["before", "after"] as const) {
      const split = moveTool(stacked, terminal.id, {
        side,
        where: edge,
        groupID: stacked[side][0].id,
      });
      expect(where(split)).toBe(side);
    }
    // And at the foot of the column.
    const tail = moveTool(stacked, terminal.id, { side, where: "end" });
    expect(where(tail)).toBe(side);
  }

  // Sharing the chat's own group is allowed too.
  const chat = findTool(start, "chat");
  const withChat = moveTool(start, terminal.id, {
    side: "center",
    where: "into",
    groupID: chat!.group.id,
  });
  const landed = findTool(withChat, terminal.id);
  expect(landed?.group.tools.map((tool) => tool.kind)).toContain("chat");
  expect(landed?.group.activeID).toBe(terminal.id);
});

test("an empty column takes no room, the centre included", () => {
  const widths = { left: 0.2, right: 0.3 };
  // Chat dragged into the left dock leaves the centre with nothing in it. It
  // used to keep filling the shell, which left a dead stripe down the middle.
  const columns = moveTool(defaultColumns(), "chat", {
    side: "left",
    where: "end",
  });
  expect(columns.center.length).toBe(0);

  const centre = columnLayout(columns, "center", widths, false);
  expect(centre.collapsed).toBe(true);
  expect(centre.basis).toBe("0px");

  // The docks take the room it was holding, keeping their proportions.
  const left = columnLayout(columns, "left", widths, false);
  expect(left.collapsed).toBe(false);
  expect(left.grow).toBe(0.2);
});

test("a drag reopens every empty column, so a panel can come back", () => {
  const columns = moveTool(defaultColumns(), "chat", {
    side: "left",
    where: "end",
  });
  const centre = columnLayout(
    columns,
    "center",
    { left: 0.2, right: 0.3 },
    true,
  );
  expect(centre.basis).toBe("9rem");
});

test("a full centre keeps its own sizing and gives the docks no extra growth", () => {
  const widths = { left: 0.2, right: 0.3 };
  const columns = defaultColumns();
  expect(columns.center.length).toBeGreaterThan(0);

  expect(columnLayout(columns, "center", widths, false).basis).toBeUndefined();
  expect(columnLayout(columns, "left", widths, false).grow).toBeUndefined();
  expect(columnLayout(columns, "right", widths, false).basis).toBe("30%");
});

test("dividers sit between columns that are both on screen", () => {
  const full = defaultColumns();
  expect(showsDivider(full, "left", false)).toBe(false); // nothing before it
  expect(showsDivider(full, "center", false)).toBe(true);
  expect(showsDivider(full, "right", false)).toBe(true);

  // With the centre collapsed the docks are neighbours and share one divider,
  // rather than stacking two against a column of no width.
  const noCentre = moveTool(full, "chat", { side: "left", where: "end" });
  expect(showsDivider(noCentre, "center", false)).toBe(false);
  expect(showsDivider(noCentre, "right", false)).toBe(true);
});

test("a new terminal never takes the id of one already in the layout", () => {
  // A layout restored from storage still holds Terminal 1; the counter used to
  // start from one again and hand out its id a second time.
  const restored = addTool(defaultColumns(), terminal, {
    side: "right",
    where: "end",
  });
  expect(nextTerminalNumber(restored)).toBe(2);

  const second: DockTool = {
    id: `terminal-${nextTerminalNumber(restored)}`,
    kind: "terminal",
    title: "Terminal 2",
  };
  const both = addTool(restored, second, { side: "right", where: "end" });
  const ids = allTools(both)
    .filter((tool) => tool.kind === "terminal")
    .map((tool) => tool.id);
  expect(new Set(ids).size).toBe(ids.length);
  expect(nextTerminalNumber(both)).toBe(3);
  // A layout with no terminals starts at one.
  expect(nextTerminalNumber(defaultColumns())).toBe(1);
});
