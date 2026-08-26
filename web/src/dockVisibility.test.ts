import {
  initialDockVisibility,
  initialYoloState,
  sessionYoloState,
} from "./dockVisibility";

test("docks start visible only when show docks preference is enabled", () => {
  expect(initialDockVisibility(true)).toBe(true);
  expect(initialDockVisibility(false)).toBe(false);
});

test("yolo starts from saved preference", () => {
  expect(initialYoloState(true)).toBe(true);
  expect(initialYoloState(false)).toBe(false);
});

test("each new session inherits saved yolo while existing sessions keep their toggle", () => {
  expect(sessionYoloState(false, true, false)).toBe(true);
  expect(sessionYoloState(false, false, true)).toBe(false);
  expect(sessionYoloState(true, true, false)).toBe(false);
});
