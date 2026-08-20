import hljs from "highlight.js";

const GUTTER_RE = /^\s*(\d+) \| (.*)$/;

export type GutteredOutput = {
  header: string;
  numbers: number[];
  code: string;
};

// read_file emits "  120 | code" (internal/agent/tools/read.go). Other tools —
// run_command, git_log — must stay plain, so a majority of lines has to match
// before the output is treated as source.
export function parseGutter(out: string): GutteredOutput | null {
  const lines = out.replace(/\n$/, "").split("\n");
  if (lines.length === 0) return null;

  let header = "";
  let body = lines;
  if (lines[0].startsWith("File status: ")) {
    header = lines[0];
    body = lines.slice(1);
  }
  if (body.length === 0) return null;

  const numbers: number[] = [];
  const code: string[] = [];
  let matched = 0;
  for (const line of body) {
    const m = GUTTER_RE.exec(line);
    if (m) {
      matched++;
      numbers.push(Number(m[1]));
      code.push(m[2]);
    } else {
      numbers.push(0);
      code.push(line);
    }
  }
  if (matched < body.length * 0.8) return null;
  return { header, numbers, code: code.join("\n") };
}

const EXT_LANG: Record<string, string> = {
  ts: "typescript",
  tsx: "typescript",
  js: "javascript",
  jsx: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  go: "go",
  rs: "rust",
  py: "python",
  rb: "ruby",
  java: "java",
  c: "c",
  h: "c",
  cc: "cpp",
  cpp: "cpp",
  hpp: "cpp",
  cs: "csharp",
  sh: "bash",
  bash: "bash",
  zsh: "bash",
  json: "json",
  yml: "yaml",
  yaml: "yaml",
  toml: "ini",
  ini: "ini",
  css: "css",
  scss: "scss",
  html: "xml",
  xml: "xml",
  md: "markdown",
  sql: "sql",
};

// Extension only. hljs.highlightAuto guesses wrong often enough on a 20-line
// fragment that plain text is the better failure mode.
export function languageForPath(text: string): string {
  const m = /([\w./-]+)\.(\w+)\b/.exec(text || "");
  if (!m) return "";
  const lang = EXT_LANG[m[2].toLowerCase()];
  if (!lang || !hljs.getLanguage(lang)) return "";
  return lang;
}

// Highlights the whole block in one call. Per-line highlighting loses parser
// state at the line boundary, which breaks template literals, block comments,
// and JSX mid-block.
export function highlightBlock(code: string, language: string): string {
  if (!language) return "";
  try {
    return hljs.highlight(code, { language, ignoreIllegals: true }).value;
  } catch {
    return "";
  }
}
