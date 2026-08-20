import { useEffect, useRef } from "react";
import type { Command } from "../commands";

// CommandPalette floats above the composer while the input starts with "/".
// Arrow keys and Enter are handled by the composer so typing never loses focus.
export function CommandPalette({
  items,
  index,
  onPick,
}: {
  items: Command[];
  index: number;
  onPick: (c: Command) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    ref.current?.querySelector<HTMLElement>(".cmd.sel")?.scrollIntoView({ block: "nearest" });
  }, [index]);

  if (items.length === 0) return null;
  return (
    <div className="palette" ref={ref}>
      {items.map((c, i) => (
        <div
          key={c.name}
          className={`cmd ${i === index ? "sel" : ""}`}
          onMouseDown={(e) => {
            e.preventDefault();
            onPick(c);
          }}
        >
          <span className="cmd-name">{c.name}</span>
          {c.arg ? <span className="cmd-arg">{c.arg}</span> : null}
          <span className="cmd-desc">{c.desc}</span>
        </div>
      ))}
    </div>
  );
}
