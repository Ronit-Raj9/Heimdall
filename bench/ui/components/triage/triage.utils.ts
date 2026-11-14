import type { Grid, Ratio, TriageGroup } from "@/lib/types/bench";

// Pure helpers for the triage screen. Patterns are observed, never root causes (docs/triage.md).

/** A pattern metric's reason split into its event ids and its summary. */
export interface TriageReason {
	eventIds: string[];
	summary: string;
}

const EVENTS_PREFIX = "events: ";

/**
 * Splits a pattern metric's `Value.Reason`, "events: id1,id2; summary", into its parts. A reason
 * without the prefix (an observed 0, or an older metric) has no event ids and is all summary.
 */
export function parseTriageReason(reason: string | null | undefined): TriageReason {
	if (!reason) return { eventIds: [], summary: "" };
	if (!reason.startsWith(EVENTS_PREFIX)) return { eventIds: [], summary: reason };
	const rest = reason.slice(EVENTS_PREFIX.length);
	const cut = rest.indexOf("; ");
	const ids = cut < 0 ? rest : rest.slice(0, cut);
	return {
		eventIds: ids
			.split(",")
			.map((id) => id.trim())
			.filter((id) => id.length > 0),
		summary: cut < 0 ? "" : rest.slice(cut + 2),
	};
}

/** Runs showing the pattern over runs where it was observed, or null with nothing observed. Unknowns never count as 0. */
export function share(ratio: Ratio | undefined): number | null {
	if (!ratio || ratio.den <= 0) return null;
	return ratio.num / ratio.den;
}

/** Second arm's share minus the first arm's, in percentage points. Null when either arm observed nothing. Descriptive only. */
export function shareDiffPoints(group: TriageGroup, armA: string, armB: string): number | null {
	const a = share(group.per_arm[armA]);
	const b = share(group.per_arm[armB]);
	if (a === null || b === null) return null;
	return (b - a) * 100;
}

/** The arms to show: the triage's own, sorted, so A comes before B. */
export function triageArms(groups: readonly TriageGroup[]): string[] {
	const arms = new Set<string>();
	for (const group of groups) {
		for (const arm of Object.keys(group.per_arm)) arms.add(arm);
		for (const arm of Object.keys(group.runs)) arms.add(arm);
	}
	return [...arms].sort();
}

/** Where a run sits in the experiment, from the grid. */
export interface RunLocation {
	runId: string;
	pairId: string;
	arm: string;
	task: string;
	condition: string;
	trial: number;
}

/** Run id -> its grid cell's pair, task, condition and trial. Cells with no counted run are skipped. */
export function runLocations(grid: Grid | undefined): Map<string, RunLocation> {
	const out = new Map<string, RunLocation>();
	for (const row of grid?.rows ?? []) {
		for (const cell of row.cells) {
			if (!cell.run_id) continue;
			out.set(cell.run_id, {
				runId: cell.run_id,
				pairId: cell.pair_id,
				arm: cell.arm,
				task: row.task,
				condition: row.condition,
				trial: cell.trial,
			});
		}
	}
	return out;
}

/** The runs showing a pattern, arm by arm in the given order, run ids sorted within an arm. */
export function patternRuns(group: TriageGroup, arms: readonly string[]): { arm: string; runId: string }[] {
	return arms.flatMap((arm) => [...(group.runs[arm] ?? [])].sort().map((runId) => ({ arm, runId })));
}