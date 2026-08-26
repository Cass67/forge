import { initialDockVisibility, initialYoloState } from "./dockVisibility";

test("docks start visible only when show docks preference is enabled", () => {
  expect(initialDockVisibility(true)).toBe(true);
  expect(initialDockVisibility(false)).toBe(false);
});

test("yolo starts from saved preference", () => {
  expect(initialYoloState(true)).toBe(true);
  expect(initialYoloState(false)).toBe(false);
});
