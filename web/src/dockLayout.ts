// Dock layout for the workspace shell.
//
// The shell is three columns — left, chat (centre) and right — and every
// column is a stack of groups. A group is a tab strip over one visible tool,
// so dropping a tab above or below a group splits that column, and dropping it
// on a tab strip stacks it. The chat is a tool like any other, which is what
// lets a terminal share a column with it.
//
// Widths are fractions of the shell, not pixels: the whole interface is scaled
// with `zoom` (see scale.ts), so a pixel width stored here would mean something
// different at every UI scale, while a fraction survives both zoom and a
// resized window. Group heights are flex weights within their column.
export type DockWidths = { left: number; right: number };
export type DockSide = "left" | "center" | "right";
export type ToolKind =
  | "explorer"
  | "editor"
  | "git"
  | "terminal"
  | "chat"
  | "preview"
  | "activity";
export type DockTool = { id: string; kind: ToolKind; title: string };
export type DockGroup = {
  id: string;
  tools: DockTool[];
  activeID: string;
  size: number;
};
export type DockColumns = Record<DockSide, DockGroup[]>;

// Where a dragged tab lands: onto a group's tabs ("into"), above or below a
// group (splitting its column), or at the foot of a column.
export type DropTarget =
  // `index` is where in the strip the tab lands; without it the tab is
  // appended, which is what a drop on the strip's empty room means.
  | { side: DockSide; where: "into"; groupID: string; index?: number }
  | { side: DockSide; where: "before" | "after"; groupID: string }
  | { side: DockSide; where: "end" };

export const DEFAULT_DOCK_WIDTHS: DockWidths = { left: 0.17, right: 0.32 };

// A dock narrower than this cannot show a file tree or a line of code, and the
// chat column needs enough room to stay readable next to them.
const MIN_DOCK = 0.08;
const MAX_DOCK = 0.6;
const MIN_CHAT = 0.2;
// A group shorter than this has no room for its own tab strip plus content.
const MIN_GROUP = 0.12;

export const DOCK_STORAGE_KEY = "forge.dockWidths";
export const LAYOUT_STORAGE_KEY = "forge.dockLayout";

export const SIDES: DockSide[] = ["left", "center", "right"];

// The tools that always exist exactly once, and the column each one falls back
// to when a stored layout has lost it.
const SINGLETONS: { tool: DockTool; home: DockSide }[] = [
  {
    tool: { id: "explorer", kind: "explorer", title: "Explorer" },
    home: "left",
  },
  { tool: { id: "git", kind: "git", title: "Source Control" }, home: "left" },
  { tool: { id: "chat", kind: "chat", title: "Chat" }, home: "center" },
  { tool: { id: "editor", kind: "editor", title: "Editor" }, home: "right" },
];

// The progress panel — context, plan and tool activity. It is a dockable panel
// like any other rather than a fixed strip beside the chat, so it can be moved,
// stacked or closed. It is not part of the default layout and not a singleton:
// the shell docks it to match the "activity panel" setting, which is what
// closing it turns off.
export const ACTIVITY_TOOL: DockTool = {
  id: "activity",
  kind: "activity",
  title: "Progress",
};

let groupCounter = 0;

export function newGroupID(): string {
  return `group-${++groupCounter}-${Math.random().toString(36).slice(2, 7)}`;
}

function makeGroup(tools: DockTool[], size = 1): DockGroup {
  return { id: newGroupID(), tools, activeID: tools[0]?.id ?? "", size };
}

function singleton(id: string): DockTool {
  return SINGLETONS.find((entry) => entry.tool.id === id)!.tool;
}

export function defaultColumns(): DockColumns {
  return {
    left: [makeGroup([singleton("explorer"), singleton("git")])],
    center: [makeGroup([singleton("chat")])],
    right: [makeGroup([singleton("editor")])],
  };
}

export function findTool(
  columns: DockColumns,
  toolID: string,
): { side: DockSide; group: DockGroup; tool: DockTool } | null {
  for (const side of SIDES) {
    for (const group of columns[side]) {
      const tool = group.tools.find((candidate) => candidate.id === toolID);
      if (tool) return { side, group, tool };
    }
  }
  return null;
}

// nextTerminalNumber is the first terminal number not already in the layout.
// The counter used to start at one on every launch while the layout came back
// from storage with Terminal 1 still in it, so the next terminal collided with
// it: same id, same key, and a pane that never received any output because the
// backend already had a shell under that id.
export function nextTerminalNumber(columns: DockColumns): number {
  let highest = 0;
  for (const tool of allTools(columns)) {
    if (tool.kind !== "terminal") continue;
    const number = Number(tool.id.replace(/^terminal-/, ""));
    if (Number.isFinite(number) && number > highest) highest = number;
  }
  return highest + 1;
}

export function allTools(columns: DockColumns): DockTool[] {
  return SIDES.flatMap((side) => columns[side].flatMap((group) => group.tools));
}

// activeAfterRemoval keeps a group's selection on a neighbour of the tab that
// left, the way closing an editor tab does.
function withoutTool(group: DockGroup, toolID: string): DockGroup {
  const index = group.tools.findIndex((tool) => tool.id === toolID);
  const tools = group.tools.filter((tool) => tool.id !== toolID);
  const activeID =
    group.activeID === toolID
      ? (tools[Math.min(index, tools.length - 1)]?.id ?? "")
      : group.activeID;
  return { ...group, tools, activeID };
}

function normalizeGroups(groups: DockGroup[]): DockGroup[] {
  const total = groups.reduce((sum, group) => sum + group.size, 0);
  if (total <= 0 || groups.length === 0) return groups;
  return groups.map((group) => ({ ...group, size: group.size / total }));
}

export function removeTool(columns: DockColumns, toolID: string): DockColumns {
  const next = {} as DockColumns;
  for (const side of SIDES) {
    next[side] = normalizeGroups(
      columns[side]
        .map((group) => withoutTool(group, toolID))
        .filter((group) => group.tools.length > 0),
    );
  }
  return next;
}

export function setActiveTool(
  columns: DockColumns,
  toolID: string,
): DockColumns {
  const found = findTool(columns, toolID);
  if (!found) return columns;
  const next = {} as DockColumns;
  for (const side of SIDES) {
    next[side] = columns[side].map((group) =>
      group.id === found.group.id ? { ...group, activeID: toolID } : group,
    );
  }
  return next;
}

export function addTool(
  columns: DockColumns,
  tool: DockTool,
  target: DropTarget,
): DockColumns {
  const groups = columns[target.side];
  if (target.where === "into") {
    const index = groups.findIndex((group) => group.id === target.groupID);
    if (index < 0)
      return addTool(columns, tool, { side: target.side, where: "end" });
    const grown = groups.map((group, at) => {
      if (at !== index) return group;
      const at2 = Math.min(
        Math.max(target.index ?? group.tools.length, 0),
        group.tools.length,
      );
      const tools = [
        ...group.tools.slice(0, at2),
        tool,
        ...group.tools.slice(at2),
      ];
      return { ...group, tools, activeID: tool.id };
    });
    return { ...columns, [target.side]: grown };
  }
  if (target.where === "end") {
    return {
      ...columns,
      [target.side]: [...groups, makeGroup([tool], groups[0]?.size ?? 1)],
    };
  }
  const index = groups.findIndex((group) => group.id === target.groupID);
  if (index < 0)
    return addTool(columns, tool, { side: target.side, where: "end" });
  // A split takes its room from the group it splits, so the rest of the column
  // keeps the heights the user dragged.
  const host = groups[index];
  const half = host.size / 2;
  const split = makeGroup([tool], half);
  const shrunk = groups.map((group, at) =>
    at === index ? { ...group, size: half } : group,
  );
  const at = target.where === "before" ? index : index + 1;
  return {
    ...columns,
    [target.side]: [...shrunk.slice(0, at), split, ...shrunk.slice(at)],
  };
}

export function moveTool(
  columns: DockColumns,
  toolID: string,
  target: DropTarget,
): DockColumns {
  const found = findTool(columns, toolID);
  if (!found) return columns;
  // Dropping a tab back where it already lives, or splitting a group off from
  // itself, would only shuffle ids around.
  if (target.where !== "end" && target.groupID === found.group.id) {
    // A tab dropped back on its own strip is a reorder.
    if (target.where === "into") {
      if (target.index === undefined) return setActiveTool(columns, toolID);
      return reorderTool(columns, found, target.index);
    }
    if (found.group.tools.length === 1) return columns;
  }
  const pruned = removeTool(columns, toolID);
  const stillThere = pruned[target.side].some(
    (group) => target.where !== "end" && group.id === target.groupID,
  );
  const landing: DropTarget =
    target.where === "end" || stillThere
      ? target
      : { side: target.side, where: "end" };
  return setActiveTool(addTool(pruned, found.tool, landing), toolID);
}

// reorderTool moves a tab within its own strip. The insertion point is read
// before the tab is lifted out, so dropping it to the right of where it
// started means what it looks like it means.
function reorderTool(
  columns: DockColumns,
  found: { side: DockSide; group: DockGroup; tool: DockTool },
  index: number,
): DockColumns {
  const from = found.group.tools.findIndex((tool) => tool.id === found.tool.id);
  const to = index > from ? index - 1 : index;
  if (to === from || from < 0) return setActiveTool(columns, found.tool.id);
  const tools = found.group.tools.filter((tool) => tool.id !== found.tool.id);
  tools.splice(Math.min(Math.max(to, 0), tools.length), 0, found.tool);
  return {
    ...columns,
    [found.side]: columns[found.side].map((group) =>
      group.id === found.group.id
        ? { ...group, tools, activeID: found.tool.id }
        : group,
    ),
  };
}

// resizeGroups splits the space two neighbours share, `fraction` being how much
// of it the upper group keeps.
export function resizeGroups(
  columns: DockColumns,
  side: DockSide,
  index: number,
  fraction: number,
): DockColumns {
  const groups = columns[side];
  if (index < 1 || index >= groups.length) return columns;
  const pair = groups[index - 1].size + groups[index].size;
  const share = Math.min(Math.max(fraction, MIN_GROUP), 1 - MIN_GROUP);
  const resized = groups.map((group, at) =>
    at === index - 1
      ? { ...group, size: pair * share }
      : at === index
        ? { ...group, size: pair * (1 - share) }
        : group,
  );
  return { ...columns, [side]: resized };
}

function reviveTool(raw: unknown): DockTool | null {
  const tool = raw as Partial<DockTool> | null;
  if (!tool || typeof tool.id !== "string" || typeof tool.title !== "string")
    return null;
  const kinds: ToolKind[] = [
    "explorer",
    "editor",
    "git",
    "terminal",
    "chat",
    "preview",
    "activity",
  ];
  if (!kinds.includes(tool.kind as ToolKind)) return null;
  return { id: tool.id, kind: tool.kind as ToolKind, title: tool.title };
}

// parseColumns is deliberately forgiving: a layout stored by an older build, or
// one that lost a panel to a crash, still has to come back as a usable shell,
// so unknown tools are dropped and missing singletons return to their home
// column.
export function parseColumns(raw: string | null): DockColumns {
  let stored: Partial<DockColumns> | null = null;
  try {
    stored = JSON.parse(raw ?? "null") as Partial<DockColumns> | null;
  } catch {
    return defaultColumns();
  }
  if (!stored || typeof stored !== "object") return defaultColumns();

  const seen = new Set<string>();
  const columns = {} as DockColumns;
  for (const side of SIDES) {
    const groups = Array.isArray(stored[side]) ? stored[side]! : [];
    columns[side] = normalizeGroups(
      groups
        .map((group) => {
          const tools = (Array.isArray(group?.tools) ? group.tools : [])
            .map(reviveTool)
            .filter((tool): tool is DockTool => tool !== null)
            .filter((tool) => {
              if (seen.has(tool.id)) return false;
              seen.add(tool.id);
              return true;
            });
          const size =
            typeof group?.size === "number" && group.size > 0 ? group.size : 1;
          const activeID = tools.some((tool) => tool.id === group?.activeID)
            ? group!.activeID
            : (tools[0]?.id ?? "");
          return {
            id: typeof group?.id === "string" ? group.id : newGroupID(),
            tools,
            activeID,
            size,
          };
        })
        .filter((group) => group.tools.length > 0),
    );
  }

  let repaired = columns;
  for (const { tool, home } of SINGLETONS) {
    if (seen.has(tool.id)) continue;
    const host = repaired[home][0];
    repaired = addTool(
      repaired,
      tool,
      host
        ? { side: home, where: "into", groupID: host.id }
        : { side: home, where: "end" },
    );
  }
  return allTools(repaired).length > 0 ? repaired : defaultColumns();
}

export function loadColumns(key = LAYOUT_STORAGE_KEY): DockColumns {
  return parseColumns(localStorage.getItem(key));
}

export function saveColumns(
  columns: DockColumns,
  key = LAYOUT_STORAGE_KEY,
): void {
  localStorage.setItem(key, JSON.stringify(columns));
}

export function clampDock(
  widths: DockWidths,
  side: "left" | "right",
  value: number,
): DockWidths {
  const other = side === "left" ? widths.right : widths.left;
  const ceiling = Math.min(MAX_DOCK, 1 - MIN_CHAT - other);
  const next = Math.min(Math.max(value, MIN_DOCK), Math.max(ceiling, MIN_DOCK));
  return side === "left"
    ? { ...widths, left: next }
    : { ...widths, right: next };
}

export function parseDockWidths(raw: string | null): DockWidths {
  try {
    const stored = JSON.parse(raw ?? "null") as Partial<DockWidths> | null;
    if (
      !stored ||
      !Number.isFinite(stored.left) ||
      !Number.isFinite(stored.right)
    )
      return DEFAULT_DOCK_WIDTHS;
    const withLeft = clampDock(DEFAULT_DOCK_WIDTHS, "left", stored.left!);
    return clampDock(withLeft, "right", stored.right!);
  } catch {
    return DEFAULT_DOCK_WIDTHS;
  }
}

export function loadDockWidths(key = DOCK_STORAGE_KEY): DockWidths {
  return parseDockWidths(localStorage.getItem(key));
}

export function saveDockWidths(
  widths: DockWidths,
  key = DOCK_STORAGE_KEY,
): void {
  localStorage.setItem(key, JSON.stringify(widths));
}

// dockFraction converts a pointer position into a fraction of the shell. The
// right dock grows as the pointer moves left, hence the mirrored form.
export function dockFraction(
  side: "left" | "right",
  clientX: number,
  rect: { left: number; width: number },
): number {
  if (rect.width <= 0) return MIN_DOCK;
  return side === "left"
    ? (clientX - rect.left) / rect.width
    : (rect.left + rect.width - clientX) / rect.width;
}

// dropZone turns a pointer inside a group's body into the drop it means: the
// outer fifths stack a column, the middle joins the group's tabs.
export function dropZone(
  clientY: number,
  rect: { top: number; height: number },
): "before" | "into" | "after" {
  if (rect.height <= 0) return "into";
  const offset = (clientY - rect.top) / rect.height;
  if (offset < 0.2) return "before";
  if (offset > 0.8) return "after";
  return "into";
}

// How much room a column gets. A column with no groups takes none at all — the
// centre included, which it used not to be: it is the column that grows to
// fill the shell, so an empty one left a dead stripe down the middle of the
// window that nothing could be put into. A drag in flight reopens every empty
// column, or a panel dragged out of the centre could never be dragged back.
export type ColumnLayout = {
  collapsed: boolean;
  // Undefined means "no explicit basis": the centre sizes itself from what the
  // docks leave behind.
  basis?: string;
  grow?: number;
};

export function columnLayout(
  columns: DockColumns,
  side: DockSide,
  widths: DockWidths,
  dragging: boolean,
): ColumnLayout {
  const empty = columns[side].length === 0;
  if (empty) {
    return { collapsed: true, basis: dragging ? "9rem" : "0px" };
  }
  const centerEmpty = columns.center.length === 0;
  return {
    collapsed: false,
    basis: side === "center" ? undefined : `${widths[side] * 100}%`,
    // With the centre gone the docks share the room it was holding, keeping
    // the proportion they already had.
    grow: centerEmpty && side !== "center" ? widths[side] : undefined,
  };
}

// A divider sits between two columns that are both on screen. With the centre
// collapsed the docks become neighbours and take the single divider between
// them, rather than two stacked against a column of no width.
export function showsDivider(
  columns: DockColumns,
  side: DockSide,
  dragging: boolean,
): boolean {
  const visible = (at: DockSide) => columns[at].length > 0 || dragging;
  if (!visible(side)) return false;
  return SIDES.slice(0, SIDES.indexOf(side)).some(visible);
}
