import { expect, test } from "bun:test";
import type { Workspace } from "./bridge";
import { labelFor, splitSections, SECTION_LIMIT } from "./sidebarSections";

const ws = (path: string, extra: Partial<Workspace> = {}): Workspace => ({
  path,
  name: path.split("/").pop() ?? path,
  threads: 1,
  last_use: "",
  active: false,
  missing: false,
  pinned: false,
  ...extra,
});

test("a unique last segment is name enough", () => {
  const labels = labelFor(["/Users/cass/git/forge", "/Users/cass/git/pool"]);
  expect(labels["/Users/cass/git/forge"]).toBe("…/forge");
  expect(labels["/Users/cass/git/pool"]).toBe("…/pool");
});

test("colliding names grow until they are told apart", () => {
  // The real case: every benchmark run ends in .../tN/forge/work.
  const paths = [
    "/Users/cass/git/agent-bench/runs_a/t1/forge/work",
    "/Users/cass/git/agent-bench/runs_a/t2/forge/work",
    "/Users/cass/git/agent-bench/runs_b/t1/forge/work",
  ];
  const labels = labelFor(paths);
  expect(new Set(Object.values(labels)).size).toBe(3);
  for (const path of paths) expect(labels[path]).toContain("work");
});

test("a path that is only its own root keeps its whole tail", () => {
  const labels = labelFor(["/tmp"]);
  expect(labels["/tmp"]).toBe("/tmp");
});

test("the long tail collapses, and the active section always survives it", () => {
  const many = Array.from({ length: 40 }, (_, i) => ws(`/w/${i}`));
  // The active one sorts last, exactly where a cap would drop it.
  many[39] = ws("/w/39", { active: true });

  const { shown, hidden } = splitSections(many, false);
  expect(shown.length).toBe(SECTION_LIMIT);
  expect(hidden).toBe(40 - SECTION_LIMIT);
  expect(shown.some((w) => w.active)).toBe(true);

  expect(splitSections(many, true)).toEqual({ shown: many, hidden: 0 });
});

test("pinned sections are kept on screen and do not eat the cap's room", () => {
  const list = [
    ws("/pin1", { pinned: true }),
    ws("/pin2", { pinned: true }),
    ...Array.from({ length: 20 }, (_, i) => ws(`/w/${i}`)),
  ];
  const { shown } = splitSections(list, false);
  expect(shown.filter((w) => w.pinned).length).toBe(2);
  expect(shown.length).toBe(SECTION_LIMIT);
});
