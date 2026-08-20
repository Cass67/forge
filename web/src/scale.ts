// UI scale.
//
// The window is a native webview with no browser zoom, and the type scale
// borrowed from secure_abc is tuned for a dense terminal app, so the whole
// interface needs one knob. `zoom` on the root element scales every px value
// at once — the stylesheet has ~185 of them, and rewriting each as rem would
// buy nothing here, where the engine is always WebKit or Blink.
export const SCALES = [0.9, 1.0, 1.15, 1.3, 1.5, 1.75] as const;

// Bigger than a browser default: this is a desktop app viewed at arm's length,
// not a web page.
export const DEFAULT_SCALE = 1.15;

const KEY = "forge.scale";
const MIN = 0.75;
const MAX = 2.0;

export function loadScale(): number {
  const v = Number(localStorage.getItem(KEY));
  return Number.isFinite(v) && v >= MIN && v <= MAX ? v : DEFAULT_SCALE;
}

export function applyScale(scale: number): void {
  document.documentElement.style.setProperty("--ui-scale", String(scale));
  localStorage.setItem(KEY, String(scale));
}

export function clampScale(scale: number): number {
  return Math.min(MAX, Math.max(MIN, Math.round(scale * 100) / 100));
}

// step moves to the next preset up or down, so the keyboard shortcuts land on
// the same values the settings panel offers.
export function step(current: number, direction: 1 | -1): number {
  const sorted = [...SCALES];
  if (direction > 0) {
    return clampScale(sorted.find((s) => s > current + 0.001) ?? MAX);
  }
  return clampScale([...sorted].reverse().find((s) => s < current - 0.001) ?? MIN);
}

export function formatScale(scale: number): string {
  return `${Math.round(scale * 100)}%`;
}
