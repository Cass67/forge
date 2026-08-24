import type { SessionStatus } from "./sessionTabs";

export type AttentionKind =
  | "approval"
  | "input"
  | "failed"
  | "plan-ready"
  | "working"
  | "unseen-done"
  | "idle";

export type Attention = {
  kind: AttentionKind;
  label: string;
  pending: number;
};

export function deriveAttention({
  pendingApprovals = 0,
  awaitingInput = false,
  failed = false,
  planReady = false,
  status = "idle",
  completedAt = 0,
  visitedAt = 0,
}: {
  pendingApprovals?: number;
  awaitingInput?: boolean;
  failed?: boolean;
  planReady?: boolean;
  status?: SessionStatus;
  completedAt?: number;
  visitedAt?: number;
}): Attention {
  if (pendingApprovals > 0)
    return {
      kind: "approval",
      label: "Approval required",
      pending: pendingApprovals,
    };
  if (awaitingInput || status === "waiting")
    return { kind: "input", label: "Awaiting input", pending: 0 };
  if (failed || status === "failed")
    return { kind: "failed", label: "Failed", pending: 0 };
  if (planReady) return { kind: "plan-ready", label: "Plan ready", pending: 0 };
  if (status === "working")
    return { kind: "working", label: "Working", pending: 0 };
  if (completedAt > visitedAt)
    return { kind: "unseen-done", label: "Completed · unseen", pending: 0 };
  return { kind: "idle", label: "Idle", pending: 0 };
}
