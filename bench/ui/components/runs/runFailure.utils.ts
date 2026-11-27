import type { Outcome, PartFailure, Run, RunDetail, Stage, ToolCall } from "@/lib/types/bench";

/** Outcomes that mean the bench or its infrastructure broke, not the agent. */
export const INFRA_OUTCOMES: readonly Outcome[] = ["aborted_infra", "infra_error", "crash"];

/** How many characters of the error the card shows before "Show all". */
export const FAILURE_PREVIEW_CHARS = 600;

/** The phase a failed stage belongs to; it picks the card's title. */
export type FailurePhase = "start" | "agent" | "grade" | "finish";

const PHASE: Record<Stage, FailurePhase> = {
	planned: "start",
	leased: "start",
	image: "start",
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
export function runFailure(run: Pick<Run, "outcome" | "failures">): PartFailure | undefined {
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
/** Why a run did not succeed, as one sentence key (uf5a runs.reason.*) and its values. */
export type RunReasonKind = "part" | "tool" | "invalidEvaluator" | "mode" | "checks" | "noChecks";

export interface RunReason {
	kind: RunReasonKind;
	params: Record<string, string | number>;
}

/** The thing a grade mode compares, for the mode sentence (uf5a runs.reason.what.*). */
export function gradeModeWhat(mode: string): "files" | "answer" | "state" | "result" {
	if (mode === "snapshot") return "files";
	if (mode === "answer" || mode === "state") return mode;
	return "result";
}

/** The first tool call that failed on the server side (5xx) or, failing that, the first errored one. */
export function failedToolCall(calls: readonly ToolCall[] | undefined): ToolCall | undefined {
	if (!calls?.length) return undefined;
	return calls.find((c) => (c.status ?? 0) >= 500) ?? calls.find((c) => c.is_error);
}

/**
 * The one-sentence reason a non-success run failed, in this order: a part failure (the run crashed), a tool
 * server error, a broken grader, a grade by comparison (mode), failed hidden checks, and last a grade that
 * ran no checks. Undefined for a pass or a run that is still going.
 */
export function runReason(detail: Pick<RunDetail, "run" | "grade" | "tool_calls" | "sealed">): RunReason | undefined {
	const { run, grade } = detail;
	if (run.outcome === "pass" || !detail.sealed) return undefined;
	const part = runFailure(run);
	if (part) return { kind: "part", params: { stage: part.stage, part: part.part ?? "" } };
	const call = failedToolCall(detail.tool_calls);
	if (call) {
		return {
			kind: "tool",
			params: { server: call.server, status: call.status ?? "an error", tool: call.tool, seq: call.seq },
		};
	}
	if (!grade) return undefined;
	if (grade.invalid_evaluator) return { kind: "invalidEvaluator", params: { reason: grade.reason ?? "" } };
	if (grade.mode) return { kind: "mode", params: { what: gradeModeWhat(grade.mode) } };
	if (grade.tests_ran > 0 && grade.failures + grade.errors > 0) {
		return { kind: "checks", params: { failures: grade.failures + grade.errors, ran: grade.tests_ran } };
	}
	if (grade.tests_ran === 0) return { kind: "noChecks", params: {} };
	return undefined;
}
