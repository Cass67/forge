// Which workspace sections the sidebar shows, and what to call them.
//
// A section is derived per distinct directory any stored thread ran in, so a
// machine that has done a few benchmark runs ends up with a hundred of them —
// most holding one thread, and 88 of them called "work" because that is the
// last segment of every `runs_*/tN/forge/work` path. The list was unreadable
// and unusable at the same time.
import type { ThreadSummary, Workspace } from "./bridge";

// How many sections to show before the rest go behind "show all". Active and
// pinned ones are always shown and do not count against it.
export const SECTION_LIMIT = 8;

const segments = (path: string) => path.split("/").filter(Boolean);

// labelFor names each path by the shortest tail that tells it apart from the
// others — "work" while it is unique, "runs_b/t1/forge/work" once it is not.
// There is no cap on how far it grows: a label that stays ambiguous is worse
// than a long one, and the row truncates with CSS anyway.

export function labelFor(paths: string[]): Record<string, string> {
  const parts = new Map(paths.map((path) => [path, segments(path)]));
  const out: Record<string, string> = {};
  for (const path of paths) {
    const own = parts.get(path) ?? [];
    let depth = 1;
    while (depth < own.length) {
      const tail = own.slice(-depth).join("/");
      const clash = paths.some(
        (other) =>
          other !== path &&
          (parts.get(other) ?? []).slice(-depth).join("/") === tail,
      );
      if (!clash) break;
      depth++;
    }
    // The name is the folder, plain: "forge", not "…/forge". A path only
    // shows through when two workspaces share a folder name, and then only as
    // far back as it takes to tell them apart. The full path is in the title.
    out[path] = own.slice(-depth).join("/") || path;
  }
  return out;
}

// splitSections keeps what the user is working with on screen and puts the
// long tail behind one toggle. A section holding the active thread is never
// hidden, whichever directory it belongs to.
export function splitSections(
  workspaces: Workspace[],
  expanded: boolean,
): { shown: Workspace[]; hidden: number } {
  if (expanded) return { shown: workspaces, hidden: 0 };

  // Being active or pinned decides whether a section is on screen, never where
  // it sits: the list keeps the order it arrived in, so selecting a workspace
  // does not bounce it to the top under the pointer.
  const keep = new Set<string>();
  for (const ws of workspaces) {
    if (ws.active || ws.pinned) keep.add(ws.path);
  }
  let room = Math.max(0, SECTION_LIMIT - keep.size);
  for (const ws of workspaces) {
    if (room === 0) break;
    if (keep.has(ws.path)) continue;
    keep.add(ws.path);
    room--;
  }
  const shown = workspaces.filter((ws) => keep.has(ws.path));
  return { shown, hidden: workspaces.length - shown.length };
}

// Manual chat ordering, per workspace. Threads the user dragged stay where
// they were put; everything else keeps its recency order below them.
export const THREAD_ORDER_KEY = "forge.threadOrder";

export function loadThreadOrders(): Record<string, string[]> {
  try {
    const raw = JSON.parse(localStorage.getItem(THREAD_ORDER_KEY) ?? "null");
    if (!raw || typeof raw !== "object") return {};
    return raw as Record<string, string[]>;
  } catch {
    return {};
  }
}

export function saveThreadOrder(dir: string, ids: string[]): void {
  const all = loadThreadOrders();
  all[dir] = ids;
  localStorage.setItem(THREAD_ORDER_KEY, JSON.stringify(all));
}

// ordered applies a saved order: ids present in it come first in that order,
// then everything else unchanged.
export function ordered<T extends { thread_id: string }>(
  list: T[],
  order: string[],
): T[] {
  if (order.length === 0) return list;
  const rank = new Map(order.map((id, i) => [id, i]));
  return [...list].sort((a, b) => {
    const ra = rank.get(a.thread_id);
    const rb = rank.get(b.thread_id);
    if (ra !== undefined || rb !== undefined) {
      return (ra ?? Infinity) - (rb ?? Infinity);
    }
    return 0;
  });
}

// Searching the sidebar. A query narrows the list to the workspaces it names,
// with their threads intact underneath, so finding a project is one word
// rather than a scroll through everything opened this year. A thread title can
// match too, in which case its workspace shows carrying only the threads that
// matched.
export type SectionMatch = { workspace: Workspace; threads: ThreadSummary[] };

function hit(haystack: string, needle: string): boolean {
  return haystack.toLowerCase().includes(needle);
}

export function searchSections(
  workspaces: Workspace[],
  threadsFor: (path: string) => ThreadSummary[],
  query: string,
): SectionMatch[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return [];
  const out: SectionMatch[] = [];
  for (const workspace of workspaces) {
    const threads = threadsFor(workspace.path);
    // A named workspace keeps everything under it: the point of searching for
    // a folder is to get at its chats.
    if (hit(workspace.name, needle) || hit(workspace.path, needle)) {
      out.push({ workspace, threads });
      continue;
    }
    const matching = threads.filter(
      (thread) =>
        hit(thread.title ?? "", needle) || hit(thread.preview ?? "", needle),
    );
    if (matching.length > 0) out.push({ workspace, threads: matching });
  }
  return out;
}
