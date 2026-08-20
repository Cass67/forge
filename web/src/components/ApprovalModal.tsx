import type { WireAction } from "../bridge";
import { DiffView } from "./DiffView";

export function ApprovalModal({
  action,
  onApprove,
  onDeny,
}: {
  action: WireAction;
  onApprove: () => void;
  onDeny: () => void;
}) {
  return (
    <div className="modal-overlay" onClick={onDeny}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <span className="modal-badge">approval required</span>
          <span className="modal-tool">{action.tool}</span>
        </div>
        <div className="modal-summary">{action.summary}</div>
        {action.detail ? <div className="modal-detail">{action.detail}</div> : null}
        {action.path ? <div className="modal-path">{action.path}</div> : null}
        {action.detail && looksLikeDiff(action.detail) ? <DiffView diff={action.detail} /> : null}
        <div className="modal-actions">
          <button className="btn danger" onClick={onDeny}>
            deny
          </button>
          <button className="btn primary" onClick={onApprove}>
            approve
          </button>
        </div>
      </div>
    </div>
  );
}

function looksLikeDiff(text: string): boolean {
  return /^(\+|-|@@|---|@@|\s)/m.test(text) && (text.includes("\n+") || text.includes("\n-") || text.includes("@@"));
}
