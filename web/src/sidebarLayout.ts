export const SIDEBAR_STORAGE_KEY = "forge.sidebarWidth";
export const DEFAULT_SIDEBAR_WIDTH = 280;

export function clampSidebarWidth(width: number, viewportWidth: number): number {
  const max = Math.max(180, viewportWidth - 320);
  const safe = Number.isFinite(width) ? width : DEFAULT_SIDEBAR_WIDTH;
  return Math.min(Math.max(safe, 180), max);
}

export function parseSidebarWidth(
  raw: string | null,
  viewportWidth: number,
): number {
  if (raw === null || raw.trim() === "")
    return clampSidebarWidth(DEFAULT_SIDEBAR_WIDTH, viewportWidth);
  return clampSidebarWidth(Number(raw), viewportWidth);
}
