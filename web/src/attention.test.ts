import { describe, expect, test } from "bun:test";
import { deriveAttention } from "./attention";

describe("deriveAttention", () => {
  test("uses product priority independently of visit state", () => {
    expect(
      deriveAttention({
        pendingApprovals: 2,
        failed: true,
        completedAt: 9,
        visitedAt: 1,
      }),
    ).toEqual({ kind: "approval", label: "Approval required", pending: 2 });
    expect(deriveAttention({ planReady: true, status: "working" }).kind).toBe(
      "plan-ready",
    );
    expect(deriveAttention({ completedAt: 9, visitedAt: 1 }).kind).toBe(
      "unseen-done",
    );
    expect(deriveAttention({ completedAt: 9, visitedAt: 9 }).kind).toBe("idle");
  });
});
