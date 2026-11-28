// Pure helpers of the Tracked tab's paid dialogs: which comparison to offer first, the spending cap
// bounds, and the cost estimate of a fix rerun. No I/O, so each is tested alone.
import type { Finding } from "@/lib/store/apis/findingsApi";
import type { ExperimentStatus } from "@/lib/types/bench";

/** The investigation spending cap bounds the server accepts, in dollars. */
export const CAP_MIN = 0.1;
export const CAP_MAX = 5;

/** The cap the investigate dialog starts with; the outer "Find out why" button names the same number (R7-5). */
export const INVESTIGATION_START_CAP_USD = 1;

/** The cap as typed, held inside the bounds; text that is not a number reads as the lowest cap. */
export function clampCap(raw: string | number): number {
	const n = Number(raw);
	if (!Number.isFinite(n) || n <= 0) return CAP_MIN;
	return Math.min(CAP_MAX, Math.max(CAP_MIN, n));
}

function when(e: ExperimentStatus): number {
	const t = Date.parse(e.finished_at ?? e.started_at ?? e.frozen_at ?? "");
	return Number.isNaN(t) ? 0 : t;
}

/** Comparisons with failed runs first, newest first, then the rest newest first: the useful ones lead the list (M7-28). */
export function investigableExperiments(experiments: readonly ExperimentStatus[]): ExperimentStatus[] {
	return [...experiments].sort((a, b) => {
		const fa = (a.slots?.failed ?? 0) > 0 ? 1 : 0;
		const fb = (b.slots?.failed ?? 0) > 0 ? 1 : 0;
		return fb - fa || when(b) - when(a);
	});
}

/** The comparison the investigation opens on: the newest with failed runs, or none. */
export function defaultInvestigation(experiments: readonly ExperimentStatus[]): string {
	const first = investigableExperiments(experiments)[0];
	return first && (first.slots?.failed ?? 0) > 0 ? first.exp_id : "";
}

/** The comparison a finding came from: the newest event of its history that names one (M7-27). */
export function findingExperiment(finding: Pick<Finding, "history" | "experiment">): string {
	if (finding.experiment) return finding.experiment;
	for (let i = finding.history.length - 1; i >= 0; i--) {
		const exp = finding.history[i]?.experiment;
		if (exp) return exp;
	}
	return "";
}

/**
 * What a fix rerun runs and about what it costs: tasks x trials x 2 agents, priced at the source
 * comparison's spend per finished run. usd is null when that comparison has no priced runs.
 */
export function rerunEstimate(
	tasks: number,
	trials: number,
	source?: Pick<ExperimentStatus, "spent_usd" | "slots">,
): { runs: number; usd: number | null } {
	const runs = tasks * trials * 2;
	const spent = source?.spent_usd?.state === "observed" ? source.spent_usd.v : null;
	const done = source?.slots?.done ?? 0;
	if (spent === null || spent === undefined || done <= 0) return { runs, usd: null };
	return { runs, usd: Math.round((spent / done) * runs * 100) / 100 };
}
/** Only the comparisons with failed runs, in investigableExperiments order: the others have nothing to read (PH2-5). */
export function failedExperiments(experiments: readonly ExperimentStatus[]): ExperimentStatus[] {
	return investigableExperiments(experiments).filter((e) => (e.slots?.failed ?? 0) > 0);
}

/**
 * The body of the findings.investigate job exactly as the server reads it (jobs_findings_w442.go
 * investigateParams, unknown fields refused): the comparison, the model and its provider, the cap
 * and the confirm. With no model picked, provider and model are empty and the server refuses.
 */
export function investigationBody(expId: string, model: { provider: string; model: string } | null, cap: number) {
	return { exp_id: expId, provider: model?.provider ?? "", model: model?.model ?? "", budget_usd: cap, yes: true };
}

/** Check fix spending cap: the estimate rounded up with room for one slow run, at least 0.25 and at most 50 dollars. */
export function rerunCap(usd: number | null): number {
	if (usd === null) return 1;
	return Math.min(50, Math.max(0.25, Math.ceil(Number((usd * 150).toFixed(6))) / 100));
}

/** The body of POST /findings/{id}/rerun as the server reads it (FindingRerunRequest). */
export function rerunBody(expId: string, arm: string, trials: number, cap: number) {
	return { exp_id: expId, arm, trials, budget_usd: cap, yes: true as const };
}
