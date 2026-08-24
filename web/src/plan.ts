import type { Entry } from "./entries";

// The agent's task plan, as the update_plan tool reports it. The tool result
// is the formatted text the TUI also renders (see react.FormatPlanState), so
// there is nothing to keep in sync: the plan on screen is whatever the last
// update_plan call said.

export type PlanStatus =
  | "pending"
  | "in_progress"
  | "completed"
  | "blocked"
  | "failed";

export type PlanStep = { title: string; status: PlanStatus; blocker?: string };
export type Plan = { explanation?: string; steps: PlanStep[] };

const STEP = /^\s*[-*\d.]+\s*\[([a-zA-Z_]+)\]\s*(.*)$/;
const BLOCKER = /\s*\(blocker:\s*(.+?)\)\s*$/;
const EXPLANATION = /^\s*Explanation:\s*(.+)$/;

const KNOWN: PlanStatus[] = [
  "pending",
  "in_progress",
  "completed",
  "blocked",
  "failed",
];

function status(raw: string): PlanStatus {
  const value = raw.trim().toLowerCase();
  return (KNOWN as string[]).includes(value)
    ? (value as PlanStatus)
    : "pending";
}

export function parsePlan(text: string): Plan {
  const steps: PlanStep[] = [];
  let explanation: string | undefined;
  for (const line of text.split("\n")) {
    const note = EXPLANATION.exec(line);
    if (note && steps.length === 0) {
      explanation = note[1].trim();
      continue;
    }
    const match = STEP.exec(line);
    if (!match) continue;
    let title = match[2].trim();
    const blocked = BLOCKER.exec(title);
    if (blocked) title = title.slice(0, blocked.index).trim();
    steps.push({
      title,
      status: status(match[1]),
      blocker: blocked ? blocked[1].trim() : undefined,
    });
  }
  return { explanation, steps };
}

// latestPlan reads the plan off the transcript: the most recent update_plan
// result wins, and a call that failed is ignored so a rejected plan never
// replaces the one still in force.
export function latestPlan(entries: Entry[]): Plan | null {
  for (let i = entries.length - 1; i >= 0; i--) {
    const entry = entries[i];
    if (entry.t !== "tool" || entry.name !== "update_plan" || entry.isError)
      continue;
    const plan = parsePlan(entry.output ?? entry.summary ?? "");
    if (plan.steps.length > 0) return plan;
  }
  return null;
}

export function planProgress(plan: Plan): { done: number; total: number } {
  return {
    done: plan.steps.filter((s) => s.status === "completed").length,
    total: plan.steps.length,
  };
}
