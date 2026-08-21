import type { DiffScope } from "./bridge";

// A view opened into the editor area from the source-control panel. The panel
// is a narrow dock; diffs and walkthroughs need the width the editor has.
export type GitTab =
  | { id: string; kind: "file"; title: string; path: string; staged: boolean }
  | { id: string; kind: "commit"; title: string; sha: string }
  | { id: string; kind: "scope"; title: string; scope: DiffScope; base: string }
  | {
      id: string;
      kind: "walkthrough";
      title: string;
      scope: DiffScope;
      base: string;
    };

export function fileTab(path: string, staged: boolean): GitTab {
  return {
    id: `file:${staged ? "s" : "w"}:${path}`,
    kind: "file",
    title: path.split("/").pop() ?? path,
    path,
    staged,
  };
}

export function commitTab(sha: string, subject: string): GitTab {
  return {
    id: `commit:${sha}`,
    kind: "commit",
    title: subject.slice(0, 40) || sha.slice(0, 8),
    sha,
  };
}

export function scopeTab(scope: DiffScope, base: string): GitTab {
  return {
    id: `scope:${scope}:${base}`,
    kind: "scope",
    title: scope === "branch" ? `branch vs ${base || "base"}` : `${scope} diff`,
    scope,
    base,
  };
}

export function walkthroughTab(scope: DiffScope, base: string): GitTab {
  return {
    id: `walk:${scope}:${base}`,
    kind: "walkthrough",
    title: "Walkthrough",
    scope,
    base,
  };
}
