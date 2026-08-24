import { expect, test } from "bun:test";
import {
  adjust,
  clampVividness,
  isDark,
  overrides,
  toHex,
  toHSL,
  vividnessLabel,
} from "./vividness";

test("hex survives the round trip through HSL", () => {
  for (const hex of ["#1e1e1e", "#d4d4d4", "#75beff", "#ff5ed2", "#000000"]) {
    const hsl = toHSL(hex);
    expect(hsl).not.toBeNull();
    expect(toHex(hsl!)).toBe(hex);
  }
  expect(toHSL("not a colour")).toBeNull();
});

test("brighter pushes ink away from the background, whichever way that is", () => {
  const darkerInk = toHSL(adjust("#d4d4d4", "ink", 5, true))!;
  expect(darkerInk.l).toBeGreaterThan(toHSL("#d4d4d4")!.l);
  // On a light theme the same request means darker ink, not lighter.
  const lightInk = toHSL(adjust("#404040", "ink", 5, false))!;
  expect(lightInk.l).toBeLessThan(toHSL("#404040")!.l);
});

test("a muddy signal regains saturation, and vivid moves further", () => {
  // A washed-out accent is exactly the case this exists for; an already
  // saturated one has nothing left to give and only gains lightness.
  const muddy = "#5a7a99";
  const base = toHSL(muddy)!;
  const bright = toHSL(adjust(muddy, "signal", 5, true))!;
  const vivid = toHSL(adjust(muddy, "signal", 10, true))!;
  expect(bright.s).toBeGreaterThan(base.s);
  expect(vivid.s).toBeGreaterThan(bright.s);

  const saturated = "#75beff";
  expect(toHSL(adjust(saturated, "signal", 10, true))!.l).toBeGreaterThan(
    toHSL(saturated)!.l,
  );
});

test("level zero is the theme untouched", () => {
  expect(adjust("#75beff", "signal", 0, true)).toBe("#75beff");
  expect(overrides(() => "#1e1e1e", 0)).toEqual({});
});

test("overrides cover ink, surfaces and signals, and nothing else", () => {
  const palette: Record<string, string> = {
    bg: "#1e1e1e",
    text: "#d4d4d4",
    muted: "#8d8d8d",
    panel: "#252526",
    panel2: "#2d2d30",
    input: "#3c3c3c",
    border: "#3e3e42",
    hover: "#2a2d2e",
    accent: "#75beff",
    ok: "#3fb950",
    err: "#f85149",
    warn: "#d29922",
  };
  const out = overrides((token) => palette[token] ?? "", 10);
  expect(Object.keys(out).sort()).toEqual(
    [
      "--accent",
      "--border",
      "--err",
      "--hover",
      "--input",
      "--muted",
      "--ok",
      "--panel",
      "--panel2",
      "--text",
      "--warn",
    ].sort(),
  );
  // The background itself is never touched: a dark theme has to stay dark.
  expect(out["--bg"]).toBeUndefined();
});

test("isDark reads the background, not the name", () => {
  expect(isDark("#1e1e1e")).toBe(true);
  expect(isDark("#ffffff")).toBe(false);
});

test("the slider position is clamped and named", () => {
  expect(clampVividness(-3)).toBe(0);
  expect(clampVividness(99)).toBe(10);
  expect(clampVividness(Number.NaN)).toBe(0);
  expect(vividnessLabel(0)).toBe("as designed");
  expect(vividnessLabel(10)).toBe("as bright as it goes");
});
