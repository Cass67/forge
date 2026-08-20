// DiffView renders a unified diff with per-line gutters and old/new line
// numbers, tracked through @@ hunk headers.
type Row = { cls: string; old: string; neu: string; text: string };

function parse(diff: string): { rows: Row[]; adds: number; dels: number } {
  const rows: Row[] = [];
  let oldNo = 0;
  let newNo = 0;
  let adds = 0;
  let dels = 0;

  for (const line of diff.split("\n")) {
    if (line.startsWith("@@")) {
      const m = /@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
      if (m) {
        oldNo = Number(m[1]);
        newNo = Number(m[2]);
      }
      rows.push({ cls: "hunk", old: "", neu: "", text: line });
    } else if (line.startsWith("---") || line.startsWith("+++") || line.startsWith("diff ")) {
      rows.push({ cls: "file", old: "", neu: "", text: line });
    } else if (line.startsWith("+")) {
      adds++;
      rows.push({ cls: "add", old: "", neu: String(newNo++), text: line.slice(1) });
    } else if (line.startsWith("-")) {
      dels++;
      rows.push({ cls: "del", old: String(oldNo++), neu: "", text: line.slice(1) });
    } else {
      const t = line.startsWith(" ") ? line.slice(1) : line;
      rows.push({ cls: "ctx", old: String(oldNo++), neu: String(newNo++), text: t });
    }
  }
  return { rows, adds, dels };
}

export function DiffView({ diff }: { diff: string }) {
  const { rows, adds, dels } = parse(diff);
  return (
    <div className="diffwrap">
      <div className="diffstat">
        <span className="d-add">+{adds}</span>
        <span className="d-del">−{dels}</span>
      </div>
      <div className="diffbox">
        {rows.map((r, i) => (
          <div key={i} className={`dl ${r.cls}`}>
            <span className="dn">{r.old}</span>
            <span className="dn">{r.neu}</span>
            <span className="dg">{r.cls === "add" ? "+" : r.cls === "del" ? "−" : " "}</span>
            <span className="dt">{r.text === "" ? " " : r.text}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
