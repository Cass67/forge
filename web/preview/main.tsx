import { useState } from "react";
import { createRoot } from "react-dom/client";
import { Transcript } from "../src/components/Transcript";
import { Composer } from "../src/components/Composer";
import { StatsBar } from "../src/components/StatsBar";
import { applyEvent, type Entry } from "../src/entries";
import { applyScale, DEFAULT_SCALE } from "../src/scale";
import "../src/styles.css";

// ?scale=1.3 exercises the real scaling path for screenshots.
applyScale(Number(new URLSearchParams(location.search).get("scale")) || DEFAULT_SCALE);

// A long fan-out transcript, rebuilt through the real reducer so the preview
// shows exactly what the app would render.
const events: { kind: string; agent?: string; text?: string; is_error?: boolean }[] = [
  { kind: "token", text: "Shortlist research now: SecureCRT baseline plus Royal TS/X, Termius, Tabby." },
  { kind: "done" },
];
for (let i = 0; i < 12; i++) {
  events.push({ kind: "tool_call", agent: "spawn_agent", text: `Researcher ${i}: gather pricing and features` });
  events.push({ kind: "tool_call", agent: "runtime", text: "" });
  events.push({ kind: "tool_result", agent: "spawn_agent", text: "ok" });
  events.push({ kind: "agent_done" });
}
events.push({ kind: "token", text: "Choose environment option above. Recommendation: Cross-platform operations." });
events.push({ kind: "done" });

const entries: Entry[] = events.reduce(
  (acc, e) => applyEvent(acc, e as never),
  [] as Entry[],
);

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
