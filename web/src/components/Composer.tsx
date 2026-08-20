import { useEffect, useRef, useState, type ClipboardEvent, type KeyboardEvent } from "react";
import { matchCommands, type Command } from "../commands";
import { CommandPalette } from "./CommandPalette";
import type { Attachment } from "../bridge";

export function Composer({
  yolo,
  onToggleYolo,
  busy,
  skills,
  history,
  attachments,
  onRemoveAttachment,
  onFiles,
  onSend,
  onCancel,
  onCommand,
}: {
  yolo: boolean;
  onToggleYolo: () => void;
  busy: boolean;
  skills: { name: string; description?: string }[];
  history: string[];
  attachments: Attachment[];
  onRemoveAttachment: (id: string) => void;
  onFiles: (files: File[]) => void;
  onSend: (text: string) => void;
  onCancel: () => void;
  onCommand: (raw: string) => void;
}) {
  const [text, setText] = useState("");
  const [index, setIndex] = useState(0);
  const [histPos, setHistPos] = useState(-1);
  const ref = useRef<HTMLTextAreaElement>(null);

  const items = matchCommands(text, skills);
  const paletteOpen = items.length > 0;

  // Grow with the content up to a cap, like a chat app rather than a form.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    // Cap in rem so the box grows with the text size rather than clipping.
    const cap = 13.75 * parseFloat(getComputedStyle(document.documentElement).fontSize || "16");
    el.style.height = Math.min(el.scrollHeight, cap) + "px";
  }, [text]);

  useEffect(() => setIndex(0), [text]);

  function onPaste(e: ClipboardEvent<HTMLTextAreaElement>) {
    const files = Array.from(e.clipboardData.files);
    if (files.length > 0) {
      e.preventDefault();
      onFiles(files);
    }
  }

  function submit(raw?: string) {
    const t = (raw ?? text).trim();
    if (!t && attachments.length === 0) return;
    setText("");
    setHistPos(-1);
    if (t.startsWith("/")) onCommand(t);
    else onSend(t);
  }

  function pick(c: Command) {
    // Commands that take an argument stay in the box for the user to complete.
    if (c.arg) setText(c.name + " ");
    else submit(c.name);
    ref.current?.focus();
  }

  function onKey(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (paletteOpen) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        return setIndex((i) => (i + 1) % items.length);
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        return setIndex((i) => (i - 1 + items.length) % items.length);
      }
      if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) {
        e.preventDefault();
        return pick(items[index]);
      }
      if (e.key === "Escape") {
        e.preventDefault();
        return setText("");
      }
    }
    // Empty input + ArrowUp recalls the previous message, shell style.
    if (e.key === "ArrowUp" && text === "" && history.length > 0) {
      e.preventDefault();
      const pos = history.length - 1;
      setHistPos(pos);
      return setText(history[pos]);
    }
    if (e.key === "ArrowUp" && histPos > 0 && text === history[histPos]) {
      e.preventDefault();
      const pos = histPos - 1;
      setHistPos(pos);
      return setText(history[pos]);
    }
    if (e.key === "ArrowDown" && histPos >= 0 && text === history[histPos]) {
      e.preventDefault();
      const pos = histPos + 1;
      if (pos >= history.length) {
        setHistPos(-1);
        return setText("");
      }
      setHistPos(pos);
      return setText(history[pos]);
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  }

  return (
    <div className="composer-wrap">
      {paletteOpen ? <CommandPalette items={items} index={index} onPick={pick} /> : null}
      {attachments.length > 0 ? (
        <div className="attachments">
          {attachments.map((a) => (
            <span className="chip" key={a.id} title={`${a.name} · ${a.width}×${a.height}`}>
              {a.name}
              <button className="chip-x" onClick={() => onRemoveAttachment(a.id)}>
                ✕
              </button>
            </span>
          ))}
        </div>
      ) : null}
      <div className={`composer ${busy ? "busy" : ""}`}>
        <textarea
          ref={ref}
          value={text}
          placeholder={busy ? "steer the running turn…" : "message forge…  /  for commands"}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKey}
          onPaste={onPaste}
          rows={1}
        />
        <div className="composer-actions">
          <button
            className={`yolo-btn ${yolo ? "on" : ""}`}
            onClick={onToggleYolo}
            title={yolo ? "Tools run without asking — click to require approval" : "Tools ask before running — click for yolo"}
          >
            yolo
          </button>
          {busy ? (
            <button className="btn danger" onClick={onCancel} title="Esc">
              Stop
            </button>
          ) : null}
          <button
            className="btn primary"
            onClick={() => submit()}
            disabled={!text.trim() && attachments.length === 0}
          >
            Send
          </button>
        </div>
      </div>
    </div>
  );
}
