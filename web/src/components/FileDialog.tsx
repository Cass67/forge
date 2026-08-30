import { useEffect, useRef, useState } from "react";

// NameDialog asks for a single path segment (file or folder name), the way the
// file manager's Create/Rename/Duplicate actions take their new name. It is a
// real dialog rather than window.prompt, which the Wails webview ignores.
export function NameDialog({
  title,
  initial = "",
  confirmLabel,
  onConfirm,
  onCancel,
}: {
  title: string;
  initial?: string;
  confirmLabel: string;
  onConfirm: (name: string) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, []);

  const submit = () => {
    const trimmed = name.trim();
    if (!trimmed) return;
    if (trimmed === "." || trimmed === ".." || trimmed.includes("/")) return;
    onConfirm(trimmed);
  };

  const invalid = !name.trim() || /(\/|^\.\.?$)/.test(name.trim());

  return (
    <div
      className="modal-overlay"
      onClick={onCancel}
      onKeyDown={(event) => {
        if (event.key === "Escape") onCancel();
      }}
    >
      <div className="modal fusedialog" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <span className="modal-badge">{title}</span>
          <button className="icon-btn close" onClick={onCancel}>
            ✕
          </button>
        </div>
        <input
          ref={input}
          aria-label={title}
          placeholder="name"
          value={name}
          onChange={(event) => setName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") submit();
          }}
        />
        <div className="modal-actions">
          <button className="btn" onClick={onCancel}>
            Cancel
          </button>
          <button className="btn primary" disabled={invalid} onClick={submit}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

// ConfirmDialog asks a yes/no question (e.g. "delete this file?"). window.confirm
// is a no-op in the Wails webview, so the file manager uses this instead.
export function ConfirmDialog({
  title,
  message,
  confirmLabel,
  onConfirm,
  onCancel,
}: {
  title: string;
  message: string;
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div
      className="modal-overlay"
      onClick={onCancel}
      onKeyDown={(event) => {
        if (event.key === "Escape") onCancel();
      }}
    >
      <div className="modal fusedialog" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <span className="modal-badge">{title}</span>
          <button className="icon-btn close" onClick={onCancel}>
            ✕
          </button>
        </div>
        <p className="fusedialog-message">{message}</p>
        <div className="modal-actions">
          <button className="btn" onClick={onCancel}>
            Cancel
          </button>
          <button className="btn danger" onClick={onConfirm}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
