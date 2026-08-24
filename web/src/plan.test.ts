import { expect, test } from "bun:test";
import { latestPlan, parsePlan, planProgress } from "./plan";
import type { Entry } from "./entries";

const formatted = `Explanation: narrowing the failure
Plan:
- [completed] Reproduce the crash
- [in_progress] Bisect the regression
- [blocked] Ship the fix (blocker: waiting on review)
- [pending] Write the test`;

test("parses the plan the tool actually emits", () => {
  const plan = parsePlan(formatted);
  expect(plan.explanation).toBe("narrowing the failure");
  expect(plan.steps.map((s) => s.status)).toEqual([
    "completed",
    "in_progress",
    "blocked",
    "pending",
  ]);
  // A blocker is carried separately rather than left glued to the title.
  expect(plan.steps[2]).toEqual({
    title: "Ship the fix",
    status: "blocked",
    blocker: "waiting on review",
  });
  expect(planProgress(plan)).toEqual({ done: 1, total: 4 });
});

test("an unknown status degrades to pending instead of vanishing", () => {
  expect(parsePlan("- [wat] Something").steps).toEqual([
    { title: "Something", status: "pending", blocker: undefined },
  ]);
});

test("the newest plan wins and a failed call is ignored", () => {
  const tool = (id: number, output: string, isError = false): Entry => ({
    id,
    t: "tool",
    name: "update_plan",
    summary: "",
    output,
    done: true,
    isError,
  });
  const entries: Entry[] = [
    tool(1, "- [pending] Old step"),
    tool(2, "- [completed] New step"),
    tool(3, "- [completed] Rejected step", true),
  ];
  expect(latestPlan(entries)?.steps[0].title).toBe("New step");
  expect(latestPlan([])).toBeNull();
  // A tool result carrying no steps is not a plan.
  expect(latestPlan([tool(4, "Plan:")])).toBeNull();
});
