import { useEffect, useMemo, useRef, useState } from "react";

// ModelPicker is a searchable overlay. The backend commonly reports 100+
// models, which a <select> cannot present usefully.
export function ModelPicker({
  models,
  current,
  onPick,
  onClose,
}: {
  models: string[];
  current: string;
  onPick: (m: string) => void;
  onClose: () => void;
}) {
  const [q, setQ] = useState("");
  const [index, setIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => inputRef.current?.focus(), []);

  const filtered = useMemo(() => {
    const needle = q.toLowerCase().trim();
    if (!needle) return models;
    return models.filter((m) => m.toLowerCase().includes(needle));
  }, [models, q]);

  useEffect(() => setIndex(0), [q]);

  // Group by provider prefix so a long flat list stays scannable.
  const groups = useMemo(() => {
    const out = new Map<string, string[]>();
    for (const m of filtered) {
      const slash = m.indexOf("/");
      const key = slash > 0 ? m.slice(0, slash) : "direct";
      const list = out.get(key) ?? [];
      list.push(m);
      out.set(key, list);
    }
    return out;
  }, [filtered]);

  function onKey(e: React.KeyboardEvent) {
    if (e.key === "Escape") return onClose();
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setIndex((i) => Math.min(filtered.length - 1, i + 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setIndex((i) => Math.max(0, i - 1));
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (filtered[index]) onPick(filtered[index]);
    }
  }

  let flat = -1;
  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal picker" onClick={(e) => e.stopPropagation()} onKeyDown={onKey}>
        <input
          ref={inputRef}
          className="picker-search"
          placeholder={`search ${models.length} models…`}
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <div className="picker-list">
          {filtered.length === 0 ? <div className="thread-empty">no match</div> : null}
          {[...groups.entries()].map(([provider, list]) => (
            <div key={provider}>
              <div className="picker-group">{provider}</div>
              {list.map((m) => {
                flat++;
                const sel = flat === index;
                return (
                  <div
                    key={m}
                    className={`picker-item ${sel ? "sel" : ""} ${m === current ? "cur" : ""}`}
                    onMouseDown={(e) => {
                      e.preventDefault();
                      onPick(m);
                    }}
                  >
                    {m}
                    {m === current ? <span className="picker-cur">current</span> : null}
                  </div>
                );
              })}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
