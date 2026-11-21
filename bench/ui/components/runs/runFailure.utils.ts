import type { Outcome, PartFailure, Run, Stage } from "@/lib/types/bench";

/** Outcomes that mean the bench or its infrastructure broke, not the agent. */
export const INFRA_OUTCOMES: readonly Outcome[] = [
  "aborted_infra",
  "infra_error",
  "crash",
];

/** How many characters of the error the card shows before "Show all". */
export const FAILURE_PREVIEW_CHARS = 600;

/** The phase a failed stage belongs to; it picks the card's title. */
export type FailurePhase = "start" | "agent" | "grade" | "finish";

const PHASE: Record<Stage, FailurePhase> = {
  planned: "start",
  leased: "start",
  prepare: "start",
  arm: "start",
  run: "agent",
  disarm: "agent",
  export: "agent",
  collect: "agent",
  grade: "grade",
  gate: "grade",
  measure: "grade",
  receipt: "finish",
  cleanup: "finish",
  seal: "finish",
};

export function failurePhase(stage: Stage): FailurePhase {
  return PHASE[stage] ?? "finish";
}

/** Hints for known error patterns, in match order. Keys index runs.failure.hint.* */
export type FailureHint = "permission" | "image" | "keySlot";

const HINTS: readonly { key: FailureHint; match: RegExp }[] = [
  { key: "permission", match: /permission denied/i },
  { key: "image", match: /image not found|pull access denied/i },
  { key: "keySlot", match: /no free key slot/i },
];

/** The hint key for an error message, or undefined when no known pattern matches. */
export function failureHint(message: string): FailureHint | undefined {
  return HINTS.find((h) => h.match.test(message))?.key;
}

/** The failure that explains a non-success run: its first part failure with an error. Undefined for a pass or a run with none. */
export function runFailure(
  run: Pick<Run, "outcome" | "failures">,
): PartFailure | undefined {
  if (run.outcome === "pass") return undefined;
  return run.failures?.find((f) => f.error?.trim());
}

/** One line of an error, for a list row: first non-empty line, cut to max characters. */
export function shortReason(message: string | undefined, max = 140): string {
  const line =
    (message ?? "")
      .split("\n")
      .find((l) => l.trim())
      ?.trim() ?? "";
  return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}
