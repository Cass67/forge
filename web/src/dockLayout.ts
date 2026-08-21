// Dock widths for the workspace shell.
//
// Widths are fractions of the shell, not pixels: the whole interface is scaled
// with `zoom` (see scale.ts), so a pixel width stored here would mean something
// different at every UI scale, while a fraction survives both zoom and a
// resized window.
export type DockWidths = { left: number; right: number };

export const DEFAULT_DOCK_WIDTHS: DockWidths = { left: 0.17, right: 0.32 };

// A dock narrower than this cannot show a file tree or a line of code, and the
// chat column needs enough room to stay readable next to them.
const MIN_DOCK = 0.08;
const MAX_DOCK = 0.6;
const MIN_CHAT = 0.2;

export const DOCK_STORAGE_KEY = "forge.dockWidths";

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
  if (rect.width <= 0) return side === "left" ? MIN_DOCK : MIN_DOCK;
  return side === "left"
    ? (clientX - rect.left) / rect.width
    : (rect.left + rect.width - clientX) / rect.width;
}
