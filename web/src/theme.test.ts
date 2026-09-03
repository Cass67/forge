import { expect, test } from "bun:test";
import { terminalTheme } from "./theme";

test("terminal palette is rebuilt from active Forge theme tokens", () => {
  const dark = terminalTheme((token) => `dark-${token}`);
  const light = terminalTheme((token) => `light-${token}`);

  expect(dark.background).toBe("dark-bg");
  expect(dark.foreground).toBe("dark-text");
  expect(light.background).toBe("light-bg");
  expect(light.cursor).toBe("light-accent");
  expect(light).not.toEqual(dark);
});
