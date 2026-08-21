import { expect, test } from "bun:test";
import {
  DEFAULT_SIDEBAR_WIDTH,
  clampSidebarWidth,
  parseSidebarWidth,
} from "./sidebarLayout";

test("sidebar width stays visible without covering the main panel", () => {
  expect(parseSidebarWidth(null, 1000)).toBe(DEFAULT_SIDEBAR_WIDTH);
  expect(clampSidebarWidth(50, 1000)).toBe(180);
  expect(clampSidebarWidth(900, 1000)).toBe(680);
  expect(parseSidebarWidth("junk", 1000)).toBe(DEFAULT_SIDEBAR_WIDTH);
});
