export type TerminalLeaf = { kind: "terminal"; id: string };
export type TerminalSplit = {
  kind: "split";
  id: string;
  direction: "horizontal" | "vertical";
  ratio: number;
  first: TerminalLayout;
  second: TerminalLayout;
};
export type TerminalLayout = TerminalLeaf | TerminalSplit;
export type TerminalTab = {
  id: string;
  title: string;
  layout: TerminalLayout;
  activePane: string;
};
export type TerminalWorkspaceState = { tabs: TerminalTab[]; activeTab: string };

export function terminalID(): string {
  return crypto.randomUUID();
}

export function newTerminalTab(number: number): TerminalTab {
  const pane = terminalID();
  return {
    id: terminalID(),
    title: `Terminal ${number}`,
    layout: { kind: "terminal", id: pane },
    activePane: pane,
  };
}

export function terminalIDs(layout: TerminalLayout): string[] {
  if (layout.kind === "terminal") return [layout.id];
  return [...terminalIDs(layout.first), ...terminalIDs(layout.second)];
}

export function splitTerminal(
  layout: TerminalLayout,
  pane: string,
  direction: TerminalSplit["direction"],
  nextPane: string,
): TerminalLayout {
  if (layout.kind === "terminal") {
    if (layout.id !== pane) return layout;
    return {
      kind: "split",
      id: terminalID(),
      direction,
      ratio: 0.5,
      first: layout,
      second: { kind: "terminal", id: nextPane },
    };
  }
  return {
    ...layout,
    first: splitTerminal(layout.first, pane, direction, nextPane),
    second: splitTerminal(layout.second, pane, direction, nextPane),
  };
}

export function splitTerminalWithLayout(
  layout: TerminalLayout,
  pane: string,
  direction: TerminalSplit["direction"],
  next: TerminalLayout,
  before = false,
): TerminalLayout {
  if (layout.kind === "terminal") {
    if (layout.id !== pane) return layout;
    return {
      kind: "split",
      id: terminalID(),
      direction,
      ratio: 0.5,
      first: before ? next : layout,
      second: before ? layout : next,
    };
  }
  return {
    ...layout,
    first: splitTerminalWithLayout(layout.first, pane, direction, next, before),
    second: splitTerminalWithLayout(layout.second, pane, direction, next, before),
  };
}

export function closeTerminalPane(
  layout: TerminalLayout,
  pane: string,
): TerminalLayout | null {
  if (layout.kind === "terminal") return layout.id === pane ? null : layout;
  const first = closeTerminalPane(layout.first, pane);
  const second = closeTerminalPane(layout.second, pane);
  if (!first) return second;
  if (!second) return first;
  return { ...layout, first, second };
}

export function resizeTerminalSplit(
  layout: TerminalLayout,
  split: string,
  ratio: number,
): TerminalLayout {
  if (layout.kind === "terminal") return layout;
  if (layout.id === split)
    return { ...layout, ratio: Math.max(0.15, Math.min(0.85, ratio)) };
  return {
    ...layout,
    first: resizeTerminalSplit(layout.first, split, ratio),
    second: resizeTerminalSplit(layout.second, split, ratio),
  };
}

function validLayout(value: unknown): value is TerminalLayout {
  if (!value || typeof value !== "object") return false;
  const item = value as Partial<TerminalLayout>;
  if (item.kind === "terminal")
    return typeof item.id === "string" && item.id.length > 0;
  return (
    item.kind === "split" &&
    typeof item.id === "string" &&
    (item.direction === "horizontal" || item.direction === "vertical") &&
    typeof item.ratio === "number" &&
    validLayout(item.first) &&
    validLayout(item.second)
  );
}

export function loadTerminalWorkspace(key: string): TerminalWorkspaceState {
  try {
    const value = JSON.parse(
      localStorage.getItem(key) ?? "null",
    ) as Partial<TerminalWorkspaceState> | null;
    if (!value || !Array.isArray(value.tabs) || value.tabs.length === 0)
      throw new Error("empty layout");
    const tabs = value.tabs.filter((tab): tab is TerminalTab =>
      Boolean(
        tab &&
        typeof tab.id === "string" &&
        typeof tab.title === "string" &&
        typeof tab.activePane === "string" &&
        validLayout(tab.layout) &&
        terminalIDs(tab.layout).includes(tab.activePane),
      ),
    );
    if (tabs.length === 0) throw new Error("invalid layout");
    return {
      tabs,
      activeTab: tabs.some((tab) => tab.id === value.activeTab)
        ? value.activeTab!
        : tabs[0].id,
    };
  } catch {
    const tab = newTerminalTab(1);
    return { tabs: [tab], activeTab: tab.id };
  }
}
