import type { WireAction } from "../bridge";
import { DiffView } from "./DiffView";

export function ApprovalModal({
  action,
  pendingCount,
  submitting,
  onApprove,
  onDeny,
}: {
  action: WireAction;
  pendingCount: number;
  submitting: boolean;
  onApprove: () => void;
  onDeny: () => void;
}) {
  return (
    <div className="modal-overlay" role="presentation">
      <div className="modal" role="alertdialog" aria-modal="true">
        <div className="modal-head">
          <span className="modal-badge">
            approval required
            {pendingCount > 1 ? ` · ${pendingCount} pending` : ""}
          </span>
          <span className="modal-tool">{action.tool}</span>
        </div>
        <div className="modal-summary">{action.summary}</div>
        {action.detail ? (
          <div className="modal-detail">{action.detail}</div>
        ) : null}
        {action.path ? <div className="modal-path">{action.path}</div> : null}
        {action.detail && looksLikeDiff(action.detail) ? (
          <DiffView diff={action.detail} />
        ) : null}
        <div className="modal-actions">
          <button className="btn danger" disabled={submitting} onClick={onDeny}>
            {submitting ? "sending…" : "deny"}
          </button>
          <button
            className="btn primary"
            disabled={submitting}
            onClick={onApprove}
          >
            {submitting ? "sending…" : "approve"}
          </button>
        </div>
      </div>
    </div>
  );
}

function looksLikeDiff(text: string): boolean {
  return (
    /^(\+|-|@@|---|@@|\s)/m.test(text) &&
    (text.includes("\n+") || text.includes("\n-") || text.includes("@@"))
  );
}
