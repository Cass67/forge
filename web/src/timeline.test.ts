import { describe, expect, test } from "bun:test";
import type { Entry } from "./entries";
import { deriveTimelineRows } from "./timeline";

describe("deriveTimelineRows", () => {
  test("pages from the tail without changing stable row keys", () => {
    const entries = [1, 2, 3].map(
      (id) => ({ id, t: "info", text: String(id) }) as Entry,
    );
    expect(deriveTimelineRows(entries, 2)).toEqual({
      rows: [
        { key: 2, entry: entries[1] },
        { key: 3, entry: entries[2] },
      ],
      hasEarlier: true,
    });
    expect(deriveTimelineRows(entries, 3).rows.map((row) => row.key)).toEqual([
      1, 2, 3,
    ]);
  });
});
