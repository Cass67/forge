import { expect, test } from "bun:test";
import type { ThreadSummary, Workspace } from "./bridge";
import {
  searchSections,
  labelFor,
  ordered,
  splitSections,
  SECTION_LIMIT,
  workspacesWithThreads,
} from "./sidebarSections";

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

test("a unique folder name is name enough, with no path decoration", () => {
  const labels = labelFor(["/Users/cass/git/forge", "/Users/cass/git/pool"]);
  expect(labels["/Users/cass/git/forge"]).toBe("forge");
  expect(labels["/Users/cass/git/pool"]).toBe("pool");
});

test("a shared folder name grows only until the two differ", () => {
  const labels = labelFor([
    "/Users/cass/git/forge/web",
    "/Users/cass/work/api/web",
    "/Users/cass/git/solo",
  ]);
  expect(labels["/Users/cass/git/forge/web"]).toBe("forge/web");
  expect(labels["/Users/cass/work/api/web"]).toBe("api/web");
  // An unambiguous one is left alone rather than padded to match.
  expect(labels["/Users/cass/git/solo"]).toBe("solo");
});

const thread = (thread_id: string) => ({ thread_id });

test("ordered puts dragged chats first, in the saved order", () => {
  const list = [thread("a"), thread("b"), thread("c")];
  expect(ordered(list, ["c"])).toEqual([thread("c"), thread("a"), thread("b")]);
  expect(ordered(list, ["b", "c"])).toEqual([
    thread("b"),
    thread("c"),
    thread("a"),
  ]);
  // Unknown ids and empty orders leave the list alone.
  expect(ordered(list, [])).toEqual(list);
  expect(ordered(list, ["zzz", "b"])).toEqual([
    thread("b"),
    thread("a"),
    thread("c"),
  ]);
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

test("a folder directly under the root is still just its name", () => {
  const labels = labelFor(["/tmp"]);
  expect(labels["/tmp"]).toBe("tmp");
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

test("selecting a workspace does not move it up the list", () => {
  const ws = (path: string, active = false): Workspace => ({
    path,
    name: path,
    threads: 1,
    last_use: "2026-08-24T00:00:00Z",
    active,
    missing: false,
    pinned: false,
  });
  const order = ["alpha", "beta", "gamma"];
  const before = splitSections(
    order.map((p) => ws(p)),
    false,
  );
  const after = splitSections(
    order.map((p) => ws(p, p === "gamma")),
    false,
  );
  expect(after.shown.map((w) => w.path)).toEqual(
    before.shown.map((w) => w.path),
  );
});

test("searching names a workspace and keeps its threads under it", () => {
  const ws = (path: string, name: string): Workspace => ({
    path,
    name,
    threads: 0,
    last_use: "2026-08-24T00:00:00Z",
    active: false,
    missing: false,
    pinned: false,
  });
  const thread = (id: string, title: string): ThreadSummary => ({
    thread_id: id,
    title,
    updated_at: "2026-08-24T00:00:00Z",
    item_count: 1,
  });
  const workspaces = [
    ws("/git/forge", "forge"),
    ws("/git/agent-bench", "agent-bench"),
  ];
  const threads: Record<string, ThreadSummary[]> = {
    "/git/forge": [thread("1", "dock layout"), thread("2", "mcp sharing")],
    "/git/agent-bench": [thread("3", "run the harness")],
  };
  const find = (query: string) =>
    searchSections(workspaces, (path) => threads[path] ?? [], query);

  // A folder match brings everything under it.
  const byFolder = find("forge");
  expect(byFolder).toHaveLength(1);
  expect(byFolder[0].threads).toHaveLength(2);

  // A thread match brings only the threads that matched.
  const byThread = find("harness");
  expect(byThread).toHaveLength(1);
  expect(byThread[0].workspace.name).toBe("agent-bench");
  expect(byThread[0].threads.map((t) => t.thread_id)).toEqual(["3"]);

  // Case and path fragments both work; an empty query hides nothing by
  // returning no matches at all, which the caller reads as "not searching".
  expect(find("GIT/")).toHaveLength(2);
  expect(find("   ")).toEqual([]);
  expect(find("nothing here")).toEqual([]);
});

test("threads stay visible under their workspace even when workspace refresh lags", () => {
  const threads: ThreadSummary[] = [{
    thread_id: "thread-1",
    title: "open",
    cwd: "/work/project",
    updated_at: "2026-08-24T00:00:00Z",
    item_count: 1,
  }];
  const result = workspacesWithThreads([], threads, "/work/active");
  expect(result.map((workspace) => workspace.path)).toEqual([
    "/work/active",
    "/work/project",
  ]);
  expect(result.find((workspace) => workspace.path === "/work/project")?.threads).toBe(1);
});

test("switched workspace stays visible when refresh has stale active flag", () => {
  const result = workspacesWithThreads(
    [ws("/work/project", { active: false })],
    [],
    "/work/project",
  );
  expect(result[0].active).toBe(true);
});
