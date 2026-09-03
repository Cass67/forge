import { expect, test } from "bun:test";
import {
  acceptSavedFile,
  DEFAULT_SCRATCH_EXPANDED,
  filterPaths,
  fuzzyScore,
  isDirty,
  type OpenFile,
} from "./workspaceFiles";

test("scratch starts collapsed", () => {
  expect(DEFAULT_SCRATCH_EXPANDED).toBe(false);
});

test("dirty files compare editor content to the saved snapshot", () => {
  const file: OpenFile = {
    path: "a.ts",
    content: "one",
    savedContent: "one",
    version: "v1",
  };
  expect(isDirty(file)).toBe(false);
  expect(isDirty({ ...file, content: "two" })).toBe(true);
});

test("a completed save preserves edits made while the write was in flight", () => {
  const file: OpenFile = {
    path: "a.ts",
    content: "second edit",
    savedContent: "old",
    version: "v1",
  };
  expect(
    acceptSavedFile(file, { content: "first edit", version: "v2" }, "v1"),
  ).toEqual({
    ...file,
    savedContent: "first edit",
    version: "v2",
  });
});

test("quick open fuzzily ranks contiguous and boundary matches", () => {
  const paths = [
    "src/components/WorkspaceShell.tsx",
    "internal/workspace.go",
    "README.md",
  ];
  expect(filterPaths(paths, "ws")).toEqual([
    "internal/workspace.go",
    "src/components/WorkspaceShell.tsx",
  ]);
  expect(fuzzyScore("README.md", "xyz")).toBeNull();
});
