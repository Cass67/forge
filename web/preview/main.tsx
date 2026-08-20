import { createRoot } from "react-dom/client";
import { Transcript } from "../src/components/Transcript";
import type { Entry } from "../src/entries";
import { applyScale, DEFAULT_SCALE } from "../src/scale";
import "../src/styles.css";

// ?scale=1.5 exercises the real scaling path for screenshots.
applyScale(Number(new URLSearchParams(location.search).get("scale")) || DEFAULT_SCALE);

const entries: Entry[] = [
  { id: 1, t: "text", role: "user", text: "think carefully about how internal/react/loop.go decides when to compact, then explain it in two sentences" },
  { id: 2, t: "tool", name: "read_file", summary: "internal/react/loop.go", done: true, output: "package react\n\nfunc x() {}\n" },
  { id: 3, t: "tool", name: "read_output", summary: '{"head":"","session":"a641d24b-77789-4715-fcc-33-2409f500","q":0,"a":16,"b":62309}', done: true, output: "some output" },
  { id: 4, t: "tool", name: "search", summary: "internal/react", done: true, output: "match" },
  { id: 5, t: "tool", name: "write_file", summary: "internal/react/compaction_manager.go", done: true, diff: "@@ -1,3 +1,4 @@\n ctx line\n-removed\n+added\n+added two\n" },
  { id: 6, t: "reasoning", text: "Checking the compaction thresholds…" },
  { id: 7, t: "text", role: "agent", agent: "forge", text: "Compaction is decided in three places: proactively before each model call (`applyProactivePromptCompaction` → `DecidePromptPressure`), which estimates the prompt.\n\n```go\nfunc Decide(n int) bool {\n\treturn n > 40\n}\n```\n" },
];

createRoot(document.getElementById("root")!).render(
  <Transcript entries={entries} busy={false} prefs={{ showTools: true, showReasoning: true, showActivity: true, showSidebar: true, scopeThreads: true }} />,
);
