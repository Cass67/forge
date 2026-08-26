import { expect, test } from "bun:test";
import { applyStatsEvent, setStatsModel } from "./sessionStats";

test("usage and context stay with the session that produced them", () => {
  let sessions = applyStatsEvent({}, "first", {
    kind: "stats",
    usage: {
      input_tokens: 120,
      output_tokens: 30,
      cached_input_tokens: 80,
    },
    context_used: 90,
    context_limit: 1000,
    duration_ms: 2000,
  });
  sessions = applyStatsEvent(sessions, "second", {
    kind: "stats",
    usage: { input_tokens: 25, output_tokens: 5 },
    context_used: 20,
  });

  expect(sessions.first).toMatchObject({
    inTok: 120,
    outTok: 30,
    cachedTok: 80,
    contextUsed: 90,
  });
  expect(sessions.second).toMatchObject({
    inTok: 25,
    outTok: 5,
    cachedTok: 0,
    contextUsed: 20,
  });
});

test("setting a session model preserves its counters", () => {
  const counted = applyStatsEvent({}, "session", {
    kind: "stats",
    usage: { input_tokens: 12, output_tokens: 3 },
  });
  const updated = setStatsModel(counted, "session", "chatgpt/gpt-5.6-sol");

  expect(updated.session).toMatchObject({
    model: "chatgpt/gpt-5.6-sol",
    inTok: 12,
    outTok: 3,
  });
});
