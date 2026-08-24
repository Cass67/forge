import type { Entry } from "./entries";

export type TimelineRow = { key: number; entry: Entry };

export function deriveTimelineRows(
  entries: Entry[],
  visibleCount: number,
): { rows: TimelineRow[]; hasEarlier: boolean } {
  const count = Math.max(1, visibleCount);
  const start = Math.max(0, entries.length - count);
  return {
    rows: entries.slice(start).map((entry) => ({ key: entry.id, entry })),
    hasEarlier: start > 0,
  };
}
