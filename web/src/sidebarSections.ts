// Which workspace sections the sidebar shows, and what to call them.
//
// A section is derived per distinct directory any stored thread ran in, so a
// machine that has done a few benchmark runs ends up with a hundred of them —
// most holding one thread, and 88 of them called "work" because that is the
// last segment of every `runs_*/tN/forge/work` path. The list was unreadable
// and unusable at the same time.
import type { Workspace } from "./bridge";

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
    const tail = own.slice(-depth);
    // A leading ellipsis says the name is a tail, not the whole path.
    out[path] = (own.length > tail.length ? "…/" : "/") + tail.join("/");
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

  const shown: Workspace[] = [];
  const rest: Workspace[] = [];
  for (const ws of workspaces) {
    if (ws.active || ws.pinned) shown.push(ws);
    else rest.push(ws);
  }
  const room = Math.max(0, SECTION_LIMIT - shown.length);
  return {
    shown: [...shown, ...rest.slice(0, room)],
    hidden: Math.max(0, rest.length - room),
  };
}
