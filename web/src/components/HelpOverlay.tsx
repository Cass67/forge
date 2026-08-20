import { COMMANDS } from "../commands";

const SHORTCUTS: [string, string][] = [
  ["Enter", "send"],
  ["Shift+Enter", "newline"],
  ["/", "command palette (at start of an empty input)"],
  ["↑", "recall previous message (empty input)"],
  ["⌘K / Ctrl+K", "model picker"],
  ["⌘, / Ctrl+,", "settings"],
  ["⌘N / Ctrl+N", "new thread"],
  ["⌘B / Ctrl+B", "toggle sidebar"],
  ["Esc", "close overlay, or cancel the running turn"],
];

export function HelpOverlay({ onClose }: { onClose: () => void }) {
  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal help" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <span className="modal-badge">help</span>
          <button className="icon-btn close" onClick={onClose}>
            ✕
          </button>
        </div>
        <div className="set-section">shortcuts</div>
        {SHORTCUTS.map(([k, v]) => (
          <div className="set-row" key={k}>
            <span className="set-k mono">{k}</span>
            <span className="set-v">{v}</span>
          </div>
        ))}
        <div className="set-section">commands</div>
        {COMMANDS.map((c) => (
          <div className="set-row" key={c.name}>
            <span className="set-k mono">
              {c.name} {c.arg ?? ""}
            </span>
            <span className="set-v">{c.desc}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
