import { useState } from "react";
import { createRoot } from "react-dom/client";
import { Transcript } from "../src/components/Transcript";
import { Composer } from "../src/components/Composer";
import { StatsBar } from "../src/components/StatsBar";
import type { Entry } from "../src/entries";
import { applyScale, DEFAULT_SCALE } from "../src/scale";
import "../src/styles.css";

// ?scale=1.3 exercises the real scaling path for screenshots.
applyScale(Number(new URLSearchParams(location.search).get("scale")) || DEFAULT_SCALE);

const entries: Entry[] = [
  { id: 1, t: "text", role: "user", text: "explain how compaction decides when to run" },
  { id: 2, t: "tool", name: "read_file", summary: "internal/react/loop.go", done: true, output: "package react" },
  { id: 3, t: "text", role: "agent", agent: "forge", text: "Compaction is decided in three places, proactively before each model call.\n\n```go\nfunc Decide(n int) bool {\n\treturn n > 40\n}\n```\n" },
];

const prefs = { showTools: true, showReasoning: true, showActivity: true, showSidebar: true, scopeThreads: true, expandReasoning: true };

function Shell() {
  const [busy] = useState(false);
  return (
    <div className="app">
      <header className="topbar">
        <span className="brand">FORGE</span>
        <span className="topbar-spacer" />
        <button className="pill">chatgpt/gpt-5.6-sol</button>
      </header>
      <div className="cols">
        <main className="center">
          <Transcript entries={entries} prefs={prefs} busy={busy} />
          <Composer
            busy={busy}
            skills={[]}
            history={[]}
            attachments={[]}
            onRemoveAttachment={() => {}}
            onFiles={() => {}}
            onSend={() => {}}
            onCancel={() => {}}
            onCommand={() => {}}
          />
        </main>
      </div>
      <StatsBar stats={{ inTok: 0, outTok: 0, contextUsed: 0, contextLimit: 0, durationMs: 0, model: "x" }} connected />
    </div>
  );
}

createRoot(document.getElementById("root")!).render(<Shell />);
