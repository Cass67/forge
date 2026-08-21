// Unified-diff parsing for the diff viewer.
//
// The parser keeps hunks intact rather than flattening to a line list: the
// viewer needs hunk boundaries to collapse unchanged context, to pair lines
// side by side, and to step between changes with the keyboard.

export type RowKind = "add" | "del" | "ctx" | "meta";

export type DiffRow = {
  kind: RowKind;
  old: number | null;
  neu: number | null;
  text: string;
  // Set by changeAnchors on the first row of each changed block, so the viewer
  // can mark scroll targets without searching the row list per render.
  anchor?: string;
  // Character ranges that differ from the paired line, for word-level
  // highlighting. Empty when the whole line is new or unchanged.
  spans?: [number, number][];
};

export type DiffHunk = {
  header: string;
  oldStart: number;
  newStart: number;
  rows: DiffRow[];
};

export type DiffFile = {
  path: string;
  oldPath: string;
  hunks: DiffHunk[];
  adds: number;
  dels: number;
  binary: boolean;
  renamed: boolean;
  added: boolean;
  deleted: boolean;
};

const HUNK = /^@@+ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;

export function parseDiff(diff: string): DiffFile[] {
  const files: DiffFile[] = [];
  let file: DiffFile | null = null;
  let hunk: DiffHunk | null = null;
  let oldNo = 0;
  let newNo = 0;

  const pushFile = () => {
    if (file) files.push(file);
  };

  // The trailing newline every diff ends with would otherwise parse as one
  // extra empty context line at the end of the last hunk.
  for (const line of diff.replace(/\n$/, "").split("\n")) {
    if (line.startsWith("diff --git ")) {
      pushFile();
      const paths = splitGitHeader(line);
      file = blankFile(paths.old, paths.neu);
      hunk = null;
      continue;
    }
    if (!file) {
      // A bare `git diff` fragment with no "diff --git" preamble still has to
      // render, so the first hunk header opens an anonymous file.
      if (!HUNK.test(line)) continue;
      file = blankFile("", "");
    }
    if (line.startsWith("--- ")) {
      const path = stripPrefix(line.slice(4));
      file.oldPath = path;
      file.added = line.slice(4).trim() === "/dev/null";
      continue;
    }
    if (line.startsWith("+++ ")) {
      const path = stripPrefix(line.slice(4));
      file.deleted = line.slice(4).trim() === "/dev/null";
      if (!file.deleted) file.path = path;
      if (!file.path) file.path = file.oldPath;
      continue;
    }
    if (line.startsWith("rename ")) {
      file.renamed = true;
      continue;
    }
    if (line.startsWith("Binary file") || line.startsWith("GIT binary patch")) {
      file.binary = true;
      continue;
    }
    const m = HUNK.exec(line);
    if (m) {
      oldNo = Number(m[1]);
      newNo = Number(m[3]);
      hunk = { header: line, oldStart: oldNo, newStart: newNo, rows: [] };
      file.hunks.push(hunk);
      continue;
    }
    if (!hunk) continue;
    if (line.startsWith("\\")) continue; // "\ No newline at end of file"
    if (line.startsWith("+")) {
      hunk.rows.push({
        kind: "add",
        old: null,
        neu: newNo++,
        text: line.slice(1),
      });
      file.adds++;
    } else if (line.startsWith("-")) {
      hunk.rows.push({
        kind: "del",
        old: oldNo++,
        neu: null,
        text: line.slice(1),
      });
      file.dels++;
    } else if (line.startsWith(" ") || line === "") {
      hunk.rows.push({
        kind: "ctx",
        old: oldNo++,
        neu: newNo++,
        text: line.slice(1),
      });
    }
  }
  pushFile();
  for (const f of files) for (const h of f.hunks) markWordSpans(h.rows);
  return files;
}

function blankFile(oldPath: string, path: string): DiffFile {
  return {
    path,
    oldPath,
    hunks: [],
    adds: 0,
    dels: 0,
    binary: false,
    renamed: oldPath !== "" && path !== "" && oldPath !== path,
    added: false,
    deleted: false,
  };
}

// splitGitHeader reads "diff --git a/x b/y". Paths containing " b/" are why
// this scans from the end rather than splitting on the first space.
function splitGitHeader(line: string): { old: string; neu: string } {
  const rest = line.slice("diff --git ".length);
  const mid = rest.lastIndexOf(" b/");
  if (mid < 0) return { old: "", neu: "" };
  return {
    old: stripPrefix(rest.slice(0, mid)),
    neu: stripPrefix(rest.slice(mid + 1)),
  };
}

function stripPrefix(path: string): string {
  const clean = path.trim().replace(/\t.*$/, "");
  if (clean === "/dev/null") return "";
  return clean.replace(/^[ab]\//, "");
}

// ---- word-level highlighting ---------------------------------------------

// markWordSpans pairs each deletion with the addition that replaced it and
// records the character ranges that actually differ, so a one-character edit
// does not light up the whole line. Only runs of equal length are paired:
// beyond that the pairing is guesswork and the noise costs more than it helps.
export function markWordSpans(rows: DiffRow[]) {
  let i = 0;
  while (i < rows.length) {
    if (rows[i].kind !== "del") {
      i++;
      continue;
    }
    let d = i;
    while (d < rows.length && rows[d].kind === "del") d++;
    let a = d;
    while (a < rows.length && rows[a].kind === "add") a++;
    const dels = d - i;
    const adds = a - d;
    if (dels > 0 && dels === adds) {
      for (let k = 0; k < dels; k++) {
        const before = rows[i + k];
        const after = rows[d + k];
        const [bSpan, aSpan] = wordSpans(before.text, after.text);
        before.spans = bSpan;
        after.spans = aSpan;
      }
    }
    i = a > i ? a : i + 1;
  }
}

// wordSpans finds the differing middle of two lines by trimming the common
// prefix and suffix at token boundaries. Returns one span per side, empty when
// the lines share nothing worth aligning.
export function wordSpans(
  before: string,
  after: string,
): [[number, number][], [number, number][]] {
  if (before === after) return [[], []];
  const a = tokenize(before);
  const b = tokenize(after);
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (
    tail < a.length - head &&
    tail < b.length - head &&
    a[a.length - 1 - tail] === b[b.length - 1 - tail]
  )
    tail++;
  // Nothing in common: highlighting the entire line adds no information.
  if (head === 0 && tail === 0) return [[], []];
  const aStart = a.slice(0, head).join("").length;
  const aEnd = before.length - a.slice(a.length - tail).join("").length;
  const bStart = b.slice(0, head).join("").length;
  const bEnd = after.length - b.slice(b.length - tail).join("").length;
  return [
    aEnd > aStart ? [[aStart, aEnd]] : [],
    bEnd > bStart ? [[bStart, bEnd]] : [],
  ];
}

// tokenize splits on identifier boundaries so a rename highlights the word
// rather than the first differing character onwards.
function tokenize(line: string): string[] {
  return line.match(/[A-Za-z0-9_$]+|\s+|[^A-Za-z0-9_$\s]/g) ?? [];
}

// ---- side-by-side pairing ------------------------------------------------

export type SplitRow = { left: DiffRow | null; right: DiffRow | null };

// splitRows lines a hunk up in two columns. A replaced block shows its
// deletions and additions on the same rows; unpaired lines get a blank facing
// cell so the columns stay in step.
export function splitRows(rows: DiffRow[]): SplitRow[] {
  const out: SplitRow[] = [];
  let i = 0;
  while (i < rows.length) {
    const row = rows[i];
    if (row.kind === "ctx" || row.kind === "meta") {
      out.push({ left: row, right: row });
      i++;
      continue;
    }
    let d = i;
    while (d < rows.length && rows[d].kind === "del") d++;
    let a = d;
    while (a < rows.length && rows[a].kind === "add") a++;
    const dels = rows.slice(i, d);
    const adds = rows.slice(d, a);
    for (let k = 0; k < Math.max(dels.length, adds.length); k++) {
      out.push({ left: dels[k] ?? null, right: adds[k] ?? null });
    }
    i = a > i ? a : i + 1;
  }
  return out;
}

// ---- navigation ----------------------------------------------------------

// changeAnchors stamps a stable id on the first row of each changed block and
// returns them in document order, so next/previous-change can step through a
// whole diff whatever the view mode or which runs are folded away.
export function changeAnchors(files: DiffFile[]): string[] {
  const anchors: string[] = [];
  files.forEach((file, fi) => {
    file.hunks.forEach((hunk, hi) => {
      let prev: RowKind | null = null;
      hunk.rows.forEach((row, ri) => {
        const changed = row.kind === "add" || row.kind === "del";
        row.anchor = undefined;
        if (changed && prev !== "add" && prev !== "del") {
          row.anchor = `d-${fi}-${hi}-${ri}`;
          anchors.push(row.anchor);
        }
        prev = row.kind;
      });
    });
  });
  return anchors;
}

export function diffStat(files: DiffFile[]): { adds: number; dels: number } {
  return files.reduce(
    (acc, f) => ({ adds: acc.adds + f.adds, dels: acc.dels + f.dels }),
    { adds: 0, dels: 0 },
  );
}
