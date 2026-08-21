// What the preview pane hands the agent when the user points at something in
// their running app: a selector, the box, the styles that were in force and
// whatever the page had been complaining about, all as one plain-text block.
export type PreviewLog = { level: string; text: string; at: number };

export type PreviewPick = {
  selector: string;
  tag: string;
  id: string;
  classes: string;
  text: string;
  html: string;
  box: { x: number; y: number; width: number; height: number };
  styles: Record<string, string>;
  url: string;
  viewport: { width: number; height: number };
  screenshot?: string;
  screenshotError?: string;
};

export const PREVIEW_TARGET_KEY = "forge.previewTarget";
export const DEFAULT_PREVIEW_TARGET = "http://localhost:5173";
// Enough console history to explain a broken screen, not enough to bury the
// message the agent actually has to read.
export const MAX_PREVIEW_LOGS = 100;
const LOGS_IN_MESSAGE = 10;

export function loadTarget(workDir: string): string {
  return (
    localStorage.getItem(`${PREVIEW_TARGET_KEY}:${workDir}`) ??
    DEFAULT_PREVIEW_TARGET
  );
}

export function saveTarget(workDir: string, target: string): void {
  localStorage.setItem(`${PREVIEW_TARGET_KEY}:${workDir}`, target);
}

export function pushLog(logs: PreviewLog[], entry: PreviewLog): PreviewLog[] {
  const next = [...logs, entry];
  return next.length > MAX_PREVIEW_LOGS
    ? next.slice(next.length - MAX_PREVIEW_LOGS)
    : next;
}

function describeElement(pick: PreviewPick): string {
  const classes = pick.classes.trim().split(/\s+/).filter(Boolean);
  return [
    pick.tag,
    pick.id ? `#${pick.id}` : "",
    classes.length ? `.${classes.join(".")}` : "",
  ].join("");
}

// formatPick keeps the shape of a bug report: what was clicked, where it sits,
// how it is styled, and what the console said, in that order.
export function formatPick(
  pick: PreviewPick,
  logs: PreviewLog[],
  note: string,
): string {
  const styles = Object.entries(pick.styles)
    .map(([key, value]) => `${key}: ${value}`)
    .join("; ");
  const recent = logs.slice(-LOGS_IN_MESSAGE);
  const lines = [
    note.trim(),
    `Preview: ${pick.url}`,
    `Element: ${describeElement(pick)}`,
    `Selector: ${pick.selector}`,
    `Box: ${pick.box.width}×${pick.box.height} at (${pick.box.x}, ${pick.box.y}) in a ${pick.viewport.width}×${pick.viewport.height} viewport`,
    styles ? `Styles: ${styles}` : "",
    pick.html ? `HTML:\n${pick.html}` : "",
    pick.screenshot
      ? "A screenshot of the page is attached."
      : `No screenshot: ${pick.screenshotError || "the page could not be rasterised"}.`,
    recent.length
      ? `Console (${recent.length} most recent):\n` +
        recent.map((log) => `[${log.level}] ${log.text}`).join("\n")
      : "",
  ];
  return lines.filter((line) => line !== "").join("\n");
}
