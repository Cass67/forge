// Splits inside one terminal panel.
//
// The dock stacks panels vertically within a column; this is the other axis,
// kept inside the terminal tool so the dock model stays as it is. A panel
// holds a binary tree: a leaf is one shell, a node is two children side by
// side ("row") or one above the other ("col").

export type TerminalPane = { id: string };
export type TerminalNode =
  | TerminalPane
  | {
      dir: "row" | "col";
      first: TerminalNode;
      second: TerminalNode;
      ratio: number;
    };

export type SplitDirection = "row" | "col";

export function isPane(node: TerminalNode): node is TerminalPane {
  return "id" in node;
}

export function paneIDs(node: TerminalNode): string[] {
  return isPane(node)
    ? [node.id]
    : [...paneIDs(node.first), ...paneIDs(node.second)];
}

// splitPane replaces one leaf with a node holding it and a new shell. The new
// pane goes second, which is what "split right" and "split down" mean.
export function splitPane(
  node: TerminalNode,
  target: string,
  dir: SplitDirection,
  newID: string,
): TerminalNode {
  if (isPane(node)) {
    if (node.id !== target) return node;
    return { dir, first: node, second: { id: newID }, ratio: 0.5 };
  }
  return {
    ...node,
    first: splitPane(node.first, target, dir, newID),
    second: splitPane(node.second, target, dir, newID),
  };
}

// closePane drops a leaf and collapses its parent onto the surviving sibling.
// Closing the last pane leaves it: a terminal panel with no shell in it is an
// empty box, and the dock's own ✕ is how you get rid of the panel.
export function closePane(node: TerminalNode, target: string): TerminalNode {
  if (isPane(node)) return node;
  if (isPane(node.first) && node.first.id === target) return node.second;
  if (isPane(node.second) && node.second.id === target) return node.first;
  return {
    ...node,
    first: closePane(node.first, target),
    second: closePane(node.second, target),
  };
}

// setRatio moves one divider. Panes are kept usable: neither side of a split
// can be squeezed past a tenth of the space.
export function setRatio(
  node: TerminalNode,
  first: string,
  ratio: number,
): TerminalNode {
  if (isPane(node)) return node;
  const clamped = Math.max(0.1, Math.min(0.9, ratio));
  if (paneIDs(node.first).includes(first) && paneIDs(node.first)[0] === first) {
    // The divider belongs to the shallowest node whose first branch starts
    // with this pane, which is the one being dragged.
    return { ...node, ratio: clamped };
  }
  return {
    ...node,
    first: setRatio(node.first, first, ratio),
    second: setRatio(node.second, first, ratio),
  };
}

const KEY = "forge.terminalSplits";

function storageKey(instanceID: string): string {
  return `${KEY}:${instanceID}`;
}

export function loadSplits(instanceID: string, rootID: string): TerminalNode {
  try {
    const raw = JSON.parse(
      localStorage.getItem(storageKey(instanceID)) ?? "null",
    );
    if (
      raw &&
      typeof raw === "object" &&
      paneIDs(raw as TerminalNode).length > 0
    ) {
      return raw as TerminalNode;
    }
  } catch {
    // A layout that will not parse is not worth reporting: one shell is the
    // right thing to fall back to.
  }
  return { id: rootID };
}

export function saveSplits(instanceID: string, node: TerminalNode): void {
  try {
    localStorage.setItem(storageKey(instanceID), JSON.stringify(node));
  } catch {
    // Storage being unavailable costs the split layout, not the terminal.
  }
}

export function forgetSplits(instanceID: string): void {
  localStorage.removeItem(storageKey(instanceID));
}
