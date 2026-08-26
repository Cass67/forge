import type { WireEvent } from "./bridge";

export type Stats = {
  inTok: number;
  outTok: number;
  cachedTok: number;
  lastOut: number;
  lastMs: number;
  contextUsed: number;
  contextLimit: number;
  durationMs: number;
  model?: string;
};

export type SessionStats = Record<string, Stats>;

export function emptyStats(model = ""): Stats {
  return {
    inTok: 0,
    outTok: 0,
    cachedTok: 0,
    lastOut: 0,
    lastMs: 0,
    contextUsed: 0,
    contextLimit: 0,
    durationMs: 0,
    model,
  };
}

export function applyStatsEvent(
  sessions: SessionStats,
  sessionID: string,
  event: WireEvent,
): SessionStats {
  if (!sessionID) return sessions;
  const hasUsage = event.kind === "stats" && event.usage;
  const hasTiming =
    event.context_used || event.context_limit || event.duration_ms;
  if (!hasUsage && !hasTiming) return sessions;

  const current = sessions[sessionID] ?? emptyStats();
  const usage = hasUsage ? event.usage : undefined;
  return {
    ...sessions,
    [sessionID]: {
      ...current,
      inTok: current.inTok + (usage?.input_tokens ?? 0),
      outTok: current.outTok + (usage?.output_tokens ?? 0),
      cachedTok: current.cachedTok + (usage?.cached_input_tokens ?? 0),
      lastOut: usage?.output_tokens || current.lastOut,
      contextUsed: event.context_used || current.contextUsed,
      contextLimit: event.context_limit || current.contextLimit,
      durationMs: event.duration_ms || current.durationMs,
      lastMs: event.duration_ms || current.lastMs,
    },
  };
}

export function setStatsModel(
  sessions: SessionStats,
  sessionID: string,
  model: string,
): SessionStats {
  if (!sessionID) return sessions;
  const current = sessions[sessionID] ?? emptyStats();
  if (current.model === model) return sessions;
  return { ...sessions, [sessionID]: { ...current, model } };
}
