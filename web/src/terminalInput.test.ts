import { expect, test } from "bun:test";
import { TerminalInputGuard } from "./terminalInput";

test("accepts one write per key event without throttling later keys", () => {
  const guard = new TerminalInputGuard();

  const first = guard.startKey("KeyL:l", 10);
  expect(first).toBeGreaterThan(0);
  expect(guard.acceptData()).toBe(true);
  expect(guard.acceptData()).toBe(false);
  expect(guard.startKey("KeyL:l", 10)).toBe(0);
  guard.endKey(first);

  const rapidRepeat = guard.startKey("KeyL:l", 10.1);
  expect(rapidRepeat).toBeGreaterThan(first);
  expect(guard.acceptData()).toBe(true);
  guard.endKey(rapidRepeat);

  expect(guard.acceptData()).toBe(true);
});
