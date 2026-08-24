// Themes are ported from secure_abc so the two apps look like siblings. Each
// one defines the same token set; components reference tokens only, never a
// raw colour. "default" is the VS Code-style dark palette secure_abc ships.
export const THEMES = [
  "default",
  "light",
  "high-contrast",
  "solarized",
  "solarized-light",
  "dracula",
  "nord",
  "gruvbox-dark",
  "gruvbox-light",
  "monokai",
  "one-dark",
  "tokyo-night",
  "catppuccin-mocha",
  "catppuccin-latte",
  "github-dark",
  "github-light",
  // Brighter darks: the ported set leans muted, and secondary text in
  // particular sat at the edge of legibility.
  "night-owl",
  "synthwave",
  "ayu-mirage",
  "cobalt",
] as const;

export type Theme = (typeof THEMES)[number];

import {
  loadVividness,
  overrides,
  saveVividness,
  type Vividness,
} from "./vividness";

const KEY = "forge.theme";

// Every token vividness may replace, so a level change can put them all back
// before recomputing from the theme underneath.
const VIVID_TOKENS = [
  "bg",
  "text",
  "muted",
  "panel",
  "panel2",
  "input",
  "border",
  "hover",
  "accent",
  "ok",
  "err",
  "warn",
] as const;

export function isTheme(v: string): v is Theme {
  return (THEMES as readonly string[]).includes(v);
}

export function loadTheme(): Theme {
  const v = localStorage.getItem(KEY) ?? "";
  return isTheme(v) ? v : "default";
}

export function applyTheme(t: Theme): void {
  const root = document.documentElement;
  root.dataset.theme = t;
  localStorage.setItem(KEY, t);
  // The vividness overrides are derived from the theme's own tokens, so they
  // have to be cleared and recomputed whenever the theme underneath changes.
  applyVividness(loadVividness());
}

// applyVividness recomputes the palette on top of whatever theme is set. It
// reads the theme's tokens back out of the document, so it works for every
// theme without knowing any of them.
export function applyVividness(level: Vividness): void {
  const root = document.documentElement;
  for (const token of VIVID_TOKENS) root.style.removeProperty(`--${token}`);
  saveVividness(level);
  if (level === 0) return;
  const computed = getComputedStyle(root);
  const next = overrides(
    (token) => computed.getPropertyValue(`--${token}`).trim(),
    level,
  );
  for (const [property, value] of Object.entries(next)) {
    root.style.setProperty(property, value);
  }
}

export function nextTheme(cur: Theme): Theme {
  return THEMES[(THEMES.indexOf(cur) + 1) % THEMES.length];
}
