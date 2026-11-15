import type {
	DifficultyHealth,
	HealthFlag,
	SuiteHealth,
	SuiteTask,
	TaskHealth,
	TaskQuarantine,
	VerifierEvidenceKind,
	VerifierHealth,
} from "@/lib/types/bench";
import type { Tone } from "./suites.utils";

// Pure helpers of suite health on the suites screen (GET /suites/{id}/health). Verifier health is
// about the grader and is a defect; outcome variance is about the agents and is normal. Nothing here
// mixes the two. Everything is testable without a browser.

/** Why health could not be shown: the server has no run store (503), no report (404), or another failure. */
export type HealthUnavailableReason = "no_store" | "not_found" | "error";

/** What the suite page knows about health: still loading, unavailable with a reason, or a report. */
export type HealthState =
	| { state: "loading" }
	| { state: "unavailable"; reason: HealthUnavailableReason; error?: unknown }
	| { state: "ready"; health: SuiteHealth; byTask: ReadonlyMap<string, TaskHealth> };

/** Verifier evidence kinds in display order. */
export const EVIDENCE_KINDS: readonly VerifierEvidenceKind[] = [
	"regrade_disagrees",
	"same_output_differs",
	"oracle_failed",
	"admission_reference",
];

/** Health flags in display order, most serious first. */
export const HEALTH_FLAGS: readonly HealthFlag[] = [
	"flaky_verifier",
	"difficulty_mismatch",
	"no_discrimination",
	"saturated",
	"dead",
	"infra_trouble",
	"timeouts",
	"insufficient_data",
];

/** Flaky verifier is the only defect that corrupts verdicts, so it is the only red flag. */
export const HEALTH_FLAG_TONE: Record<HealthFlag, Tone> = {
	flaky_verifier: "bad",
	difficulty_mismatch: "warn",
	no_discrimination: "warn",
	saturated: "warn",
	dead: "warn",
	infra_trouble: "warn",
	timeouts: "warn",
	insufficient_data: "neutral",
};

/** The HTTP status of an RTK Query error, when it has one. */
function errorStatus(error: unknown): unknown {
	return error && typeof error === "object" && "status" in error ? (error as { status: unknown }).status : undefined;
}

/** 503 means the server has no run store; 404 an unknown suite or a server without the route. */
export function healthUnavailableReason(error: unknown): HealthUnavailableReason {
	const status = errorStatus(error);
	if (status === 503) return "no_store";
	if (status === 404) return "not_found";
	return "error";
}

/** Task id to its health, for the table and the detail panel. */
export function healthByTask(health: SuiteHealth): Map<string, TaskHealth> {
	return new Map((health.tasks ?? []).map((task) => [task.task, task]));
}

/** The page's health state from a query result. A failure never throws: it becomes "unavailable". */
export function healthState(query: { isLoading: boolean; isError: boolean; error?: unknown; data?: SuiteHealth }): HealthState {
	if (query.data && !query.isError) return { state: "ready", health: query.data, byTask: healthByTask(query.data) };
	if (query.isLoading) return { state: "loading" };
	if (query.isError) return { state: "unavailable", reason: healthUnavailableReason(query.error), error: query.error };
	return { state: "unavailable", reason: "error" };
}

/** True for a held-out task, whether or not the server serves its content. */
export function isHeldOut(task: Pick<SuiteTask, "split">): boolean {
	return task.split === "held_out";
}

/** True when the server withheld the task's title, files and checks: show its id only, never a detail link. */
export function isRedacted(task: Pick<SuiteTask, "redacted">): boolean {
	return task.redacted === true;
}

/** The task's quarantine from the suite list, else from the health report; null when it is in plans. */
export function taskQuarantine(task: Pick<SuiteTask, "quarantine"> | undefined, health: TaskHealth | undefined): TaskQuarantine | null {
	return task?.quarantine ?? health?.quarantine ?? null;
}

/** Tasks the summary should count: the suite list's counts when the server sent them, else counted from tasks. */
export function heldOutAndQuarantined(suite: {
	held_out_count?: number;
	quarantined_count?: number;
	tasks: readonly Pick<SuiteTask, "split" | "quarantine">[];
}): { heldOut: number; quarantined: number } {
	return {
		heldOut: suite.held_out_count ?? suite.tasks.filter(isHeldOut).length,
		quarantined: suite.quarantined_count ?? suite.tasks.filter((task) => task.quarantine).length,
	};
}

/** Evidence counted per kind, in EVIDENCE_KINDS order; an unknown kind is kept, after them. */
export function evidenceKinds(verifier: Pick<VerifierHealth, "evidence">): { kind: string; count: number }[] {
	const counts = new Map<string, number>();
	for (const item of verifier.evidence ?? []) counts.set(item.kind, (counts.get(item.kind) ?? 0) + 1);
	const known = EVIDENCE_KINDS.filter((kind) => counts.has(kind));
	const unknown = [...counts.keys()].filter((kind) => !EVIDENCE_KINDS.includes(kind as VerifierEvidenceKind)).sort();
	return [...known, ...unknown].map((kind) => ({ kind, count: counts.get(kind) ?? 0 }));
}

/** A task every config always passes, or always fails, says nothing about which agent is better. */
export function hasNoInformation(difficulty: Pick<DifficultyHealth, "discrimination">): boolean {
	return difficulty.discrimination === "always_pass" || difficulty.discrimination === "always_fail";
}

/** A rate in [0, 1] as a percent: one decimal below 10%, whole numbers above; null stays null. */
export function formatPercent(rate: number | null | undefined, locale: string): string | null {
	if (rate === null || rate === undefined || !Number.isFinite(rate)) return null;
	const digits = rate > 0 && rate < 0.1 ? 1 : 0;
	return rate.toLocaleString(locale, { style: "percent", maximumFractionDigits: digits });
}

/** One chip of the health column: a flag, the quarantine, or the overall state. */
export interface HealthChip {
	key: "quarantined" | HealthFlag;
	tone: Tone;
}

/**
 * The chips of a task's health cell, most serious first: quarantine, then each flag. no_discrimination
 * already says saturated or dead, so those two are dropped when it is present. Variance is never a
 * chip: it is agent behavior, not a task defect.
 */
export function healthChips(health: TaskHealth | undefined, quarantine: TaskQuarantine | null): HealthChip[] {
	const chips: HealthChip[] = quarantine ? [{ key: "quarantined", tone: "warn" }] : [];
	if (!health) return chips;
	const flags = new Set<HealthFlag>(health.flags ?? []);
	if (health.verifier?.flaky) flags.add("flaky_verifier");
	if (flags.has("no_discrimination")) {
		flags.delete("saturated");
		flags.delete("dead");
	}
	const ordered = [...HEALTH_FLAGS.filter((flag) => flags.has(flag))];
	return [...chips, ...ordered.map((flag) => ({ key: flag, tone: HEALTH_FLAG_TONE[flag] }))];
}

/** The tone of a difficulty comparison: a mismatch is highlighted, the rest are plain. */
export function difficultyMatchTone(match: DifficultyHealth["match"]): Tone {
	return match === "mismatch" ? "warn" : match === "match" ? "good" : "neutral";
}

/** Flag counts of the report, in HEALTH_FLAGS order, leaving out zeros; an unknown flag is kept, after them. */
export function flagCounts(counts: Record<string, number> | null | undefined): { flag: string; count: number }[] {
	const entries = Object.entries(counts ?? {}).filter(([, count]) => count > 0);
	const byFlag = new Map(entries);
	const known = HEALTH_FLAGS.filter((flag) => byFlag.has(flag));
	const unknown = [...byFlag.keys()].filter((flag) => !HEALTH_FLAGS.includes(flag as HealthFlag)).sort();
	return [...known, ...unknown].map((flag) => ({ flag, count: byFlag.get(flag) ?? 0 }));
}