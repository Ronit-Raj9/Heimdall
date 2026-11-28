import { benchApi } from "@/lib/store/apis/benchApi";
import type { EvaluationRerunResult } from "@/lib/store/apis/runControlApi";
import type { ReportTotals } from "@/lib/store/apis/evaluationsApi";

/** The smallest budget the wizard or a paid action suggests, in USD. */
export const MIN_SUGGESTED_USD = 0.25;

/** A suggested cap is about this many times the estimate. */
export const SUGGEST_FACTOR = 3;

/** The cap a paid action offers when no estimate is known, in USD (one click under the owner's rule). */
export const UNKNOWN_ESTIMATE_CAP_USD = 1;

/**
 * The suggested budget for an estimate (round 4, F5): about 3 times the estimate, at least $0.25, rounded up to
 * $0.05 under $1, to $0.25 under $10 and to whole dollars above. Null when the estimate is not known.
 */
export function suggestedBudget(estimateUsd: number | null | undefined): number | null {
	if (estimateUsd === null || estimateUsd === undefined || !Number.isFinite(estimateUsd) || estimateUsd < 0) return null;
	const raw = Math.max(MIN_SUGGESTED_USD, estimateUsd * SUGGEST_FACTOR);
	const step = raw < 1 ? 0.05 : raw < 10 ? 0.25 : 1;
	return Math.round(Math.ceil(raw / step - 1e-9) * step * 100) / 100;
}

/** The mean cost of one run over the report's totals, or null when any arm's cost is not known or nothing ran. */
export function costPerRun(totals: Record<string, ReportTotals> | undefined): number | null {
	let cost = 0;
	let runs = 0;
	for (const t of Object.values(totals ?? {})) {
		if (t.cost_usd === null || t.cost_usd === undefined) return null;
		cost += t.cost_usd;
		runs += t.cost_runs ?? t.runs;
	}
	return runs > 0 ? cost / runs : null;
}

/** Runs times the cost of one run, or null when the cost of a run is not known. */
export function estimateOf(runs: number, perRun: number | null): number | null {
	return perRun === null || !(runs > 0) ? null : runs * perRun;
}

/** The body of POST /evaluations/{id}/cheat-check (the server refuses one without budget_usd). */
export function cheatCheckBody(budgetUsd: number): { yes: true; budget_usd: number } {
	return { yes: true, budget_usd: budgetUsd };
}

/** The body of POST /evaluations/{id}/rerun-failures with its cap. */
export function rerunFailuresBody(budgetUsd: number): { budget_usd: number } {
	return { budget_usd: budgetUsd };
}

/**
 * The paid starts of an evaluation with their cap (round 4, F5): Rerun failures and the cheat check send
 * budget_usd, which the server requires for the cheat check and splits over the rerun's benchmarks.
 */
export const evaluationCapsApi = benchApi.enhanceEndpoints({ addTagTypes: ["Evaluations"] }).injectEndpoints({
	endpoints: (builder) => ({
		rerunEvaluationFailuresCapped: builder.mutation<EvaluationRerunResult, { evalId: string; budgetUsd: number }>({
			query: ({ evalId, budgetUsd }) => ({
				url: `/evaluations/${encodeURIComponent(evalId)}/rerun-failures`,
				method: "POST",
				body: rerunFailuresBody(budgetUsd),
			}),
			invalidatesTags: [{ type: "Evaluations", id: "LIST" }],
		}),
		startCheatCheckCapped: builder.mutation<{ id: string }, { evalId: string; budgetUsd: number }>({
			query: ({ evalId, budgetUsd }) => ({
				url: `/evaluations/${encodeURIComponent(evalId)}/cheat-check`,
				method: "POST",
				body: cheatCheckBody(budgetUsd),
			}),
			invalidatesTags: [{ type: "Evaluations", id: "LIST" }],
		}),
	}),
});

export const { useRerunEvaluationFailuresCappedMutation, useStartCheatCheckCappedMutation } = evaluationCapsApi;

/** What an evaluation spent per run so far: its benchmarks' spend over the stored result's runs; null when unknown. */
export function evaluationCostPerRun(evaluation: {
	children?: { spent_usd?: number }[];
	result?: { arms: { runs: number }[]; cost_usd?: number };
}): number | null {
	const runs = (evaluation.result?.arms ?? []).reduce((n, a) => n + (a.runs || 0), 0);
	if (!(runs > 0)) return null;
	const stored = evaluation.result?.cost_usd;
	const spent =
		typeof stored === "number"
			? stored
			: (evaluation.children ?? []).reduce<number | null>((s, c) => (s === null || typeof c.spent_usd !== "number" ? null : s + c.spent_usd), 0);
	return spent === null ? null : spent / runs;
}
