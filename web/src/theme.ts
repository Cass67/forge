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

const KEY = "forge.theme";

export function isTheme(v: string): v is Theme {
  return (THEMES as readonly string[]).includes(v);
}

export function loadTheme(): Theme {
  const v = localStorage.getItem(KEY) ?? "";
  return isTheme(v) ? v : "default";
}

export function applyTheme(t: Theme): void {
  document.documentElement.dataset.theme = t;
  localStorage.setItem(KEY, t);
}

export function nextTheme(cur: Theme): Theme {
  return THEMES[(THEMES.indexOf(cur) + 1) % THEMES.length];
}
