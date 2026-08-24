// Dark themes read muddy for two reasons that are not the colours themselves:
// text sits too close to its background, and the accents are desaturated by
// the time they land on a dark surface. Rather than hand-editing twenty
// palettes, the tokens are recomputed at apply time — text is pushed away from
// the background, surfaces are separated from each other, and the accent
// colours get their saturation back.

// A slider position, not a mode: 0 leaves the theme exactly as designed and
// MAX_VIVIDNESS is as far as it goes before colours stop being the theme's.
export const MAX_VIVIDNESS = 10;
export type Vividness = number;

export function clampVividness(level: number): Vividness {
  if (!Number.isFinite(level)) return 0;
  return Math.max(0, Math.min(MAX_VIVIDNESS, Math.round(level)));
}

// What the slider position says, so a number on its own does not have to.
export function vividnessLabel(level: Vividness): string {
  if (level <= 0) return "as designed";
  if (level <= 3) return "a little brighter";
  if (level <= 6) return "brighter";
  if (level <= 8) return "vivid";
  return "as bright as it goes";
}

const KEY = "forge.vividness";

// Tokens that carry text, the ones that carry a surface, and the ones that
// exist to be seen. Each is pushed in its own direction.
const INK = ["text", "muted"] as const;
const SURFACE = ["panel", "panel2", "input", "border", "hover"] as const;
const SIGNAL = ["accent", "ok", "err", "warn"] as const;
// The page behind everything. It moves opposite to the ink: deeper on a dark
// theme, paler on a light one, which is what buys the contrast at the top of
// the slider.
const GROUND = ["bg"] as const;

export function loadVividness(): Vividness {
  return clampVividness(Number(localStorage.getItem(KEY) ?? "0"));
}

export function saveVividness(level: Vividness): void {
  localStorage.setItem(KEY, String(level));
}

type HSL = { h: number; s: number; l: number };

export function toHSL(colour: string): HSL | null {
  const hex = colour.trim().replace("#", "");
  const full =
    hex.length === 3
      ? hex
          .split("")
          .map((c) => c + c)
          .join("")
      : hex;
  if (!/^[0-9a-fA-F]{6}$/.test(full)) return null;
  const r = parseInt(full.slice(0, 2), 16) / 255;
  const g = parseInt(full.slice(2, 4), 16) / 255;
  const b = parseInt(full.slice(4, 6), 16) / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  if (max === min) return { h: 0, s: 0, l };
  const d = max - min;
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
  const h =
    max === r
      ? ((g - b) / d + (g < b ? 6 : 0)) / 6
      : max === g
        ? ((b - r) / d + 2) / 6
        : ((r - g) / d + 4) / 6;
  return { h, s, l };
}

export function toHex({ h, s, l }: HSL): string {
  const f = (n: number) => {
    const k = (n + h * 12) % 12;
    const a = s * Math.min(l, 1 - l);
    const v = l - a * Math.max(-1, Math.min(k - 3, 9 - k, 1));
    return Math.round(255 * v)
      .toString(16)
      .padStart(2, "0");
  };
  return `#${f(0)}${f(8)}${f(4)}`;
}

const clamp = (v: number) => Math.max(0, Math.min(1, v));

// isDark decides which way "brighter" points: away from the background, which
// on a light theme means darker ink, not lighter.
export function isDark(background: string): boolean {
  const hsl = toHSL(background);
  return hsl ? hsl.l < 0.4 : true;
}

export function adjust(
  colour: string,
  role: "ink" | "surface" | "signal" | "ground",
  level: Vividness,
  dark: boolean,
): string {
  const hsl = toHSL(colour);
  if (!hsl || level <= 0) return colour;
  // The slider runs to MAX_VIVIDNESS; the steps below are per half-scale, so
  // the midpoint is a gentle lift and the end is a strong one.
  const step = clampVividness(level) / 5;
  const away = dark ? 1 : -1;
  if (role === "ink") {
    // Ink moves away from the background it sits on, and keeps a little of
    // its own colour rather than bleaching towards white as it climbs.
    return toHex({
      ...hsl,
      s: clamp(hsl.s + 0.05 * step),
      l: clamp(hsl.l + away * 0.09 * step),
    });
  }
  if (role === "surface") {
    // Surfaces only need to separate from each other. Lifting them hard is
    // what turns a dark theme into grey mush, so they move least.
    return toHex({ ...hsl, l: clamp(hsl.l + away * 0.02 * step) });
  }
  if (role === "ground") {
    // The background goes the other way: deepening it is what gives the ink
    // and the accents somewhere to stand out against, and it is why the top
    // of the slider reads as brighter rather than washed out.
    return toHex({ ...hsl, l: clamp(hsl.l - away * 0.025 * step) });
  }
  // Signals get their saturation back, which is most of what "muddy" means.
  return toHex({
    ...hsl,
    s: clamp(hsl.s + 0.2 * step),
    l: clamp(hsl.l + away * 0.05 * step),
  });
}

// overrides reads the theme's own tokens and returns the values to set on top
// of them. Reading rather than hard-coding is what lets this work for every
// theme, including any added later.
export function overrides(
  read: (token: string) => string,
  level: Vividness,
): Record<string, string> {
  if (level <= 0) return {};
  const dark = isDark(read("bg"));
  const out: Record<string, string> = {};
  const apply = (
    tokens: readonly string[],
    role: "ink" | "surface" | "signal" | "ground",
  ) => {
    for (const token of tokens) {
      const current = read(token);
      const next = adjust(current, role, level, dark);
      if (next !== current) out[`--${token}`] = next;
    }
  };
  apply(INK, "ink");
  apply(SURFACE, "surface");
  apply(SIGNAL, "signal");
  apply(GROUND, "ground");
  return out;
}
