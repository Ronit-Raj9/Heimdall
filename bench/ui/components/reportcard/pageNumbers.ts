import type { Evaluation, Report, ReportTotals } from "@/lib/store/apis/evaluationsApi";
import { formatUsd } from "@/lib/money";
import { MIN_RUNS_TO_RATE, pct, scoreRange } from "./reportCard.utils";

/**
 * The numbers the evaluation page shows in more than one place (Q-14, Q-15), each from ONE helper with
 * one definition, so the subtitle, the report card, the benchmark rows and the release block agree.
 */

/** Report totals with the fault cost the server adds (M7-89); absent on an older server. */
type TotalsWithFaultCost = ReportTotals & { fault_cost_usd?: number | null };

/** Runs of a report: normal (clean) runs, runs with a fault injected, and all of them, summed over arms. */
export function reportRunCounts(report: Report | undefined): { normal: number; fault: number; all: number } | undefined {
	const totals = Object.values(report?.totals ?? {});
	if (totals.length === 0) return undefined;
	const normal = totals.reduce((n, t) => n + (t?.runs ?? 0), 0);
	const fault = totals.reduce((n, t) => n + (t?.fault_runs ?? 0), 0);
	return { normal, fault, all: normal + fault };
}

/** The cost of every run of a report (normal plus fault runs), or null when any part is not known. */
export function reportCostUsd(report: Report | undefined): number | null {
	const totals = Object.values(report?.totals ?? {}) as TotalsWithFaultCost[];
	if (totals.length === 0) return null;
	let sum = 0;
	for (const t of totals) {
		if (typeof t.cost_usd !== "number") return null;
		sum += t.cost_usd;
		if ((t.fault_runs ?? 0) > 0) {
			if (typeof t.fault_cost_usd !== "number") return null;
			sum += t.fault_cost_usd;
		}
	}
	return sum;
}

/** What the benchmarks of an evaluation spent, summed (the figure the budget counts); null when none says. */
function childrenSpend(evaluation: Evaluation): number | null {
	const children = (evaluation.children ?? []).filter((c) => typeof c.spent_usd === "number");
	if (children.length === 0) return null;
	return children.reduce((s, c) => s + (c.spent_usd ?? 0), 0);
}

/**
 * The run count and the spend of a whole evaluation, the one definition the page uses: every run (normal and
 * with faults) and every dollar spent. Spend is what the benchmarks report spent, else the report's cost of
 * all runs, else the stored result. Runs come from the report; undefined until it has loaded.
 */
export function evaluationTotals(evaluation: Evaluation | undefined, report?: Report): { runs?: number; costUsd: number | null } {
	const runs = reportRunCounts(report)?.all;
	const fromChildren = evaluation ? childrenSpend(evaluation) : null;
	const costUsd = fromChildren ?? reportCostUsd(report) ?? (typeof evaluation?.result?.cost_usd === "number" ? evaluation.result.cost_usd : null);
	return { runs, costUsd };
}

/** "$0.0098 for 6 runs", the total cost line; undefined when the cost or the count is not known. */
export function totalCostText(totals: { runs?: number; costUsd: number | null }): string | undefined {
	if (typeof totals.costUsd !== "number" || !(totals.runs && totals.runs > 0)) return undefined;
	return `${formatUsd(totals.costUsd)} for ${totals.runs} ${totals.runs === 1 ? "run" : "runs"}`;
}

/** Fixed words of a rate under MIN_RUNS_TO_RATE runs: the same on every part of the page. */
export const TOO_FEW_TO_RATE = "too few runs to rate";

/**
 * One rate as the page prints it: under 10 runs "2 of 3, too few runs to rate" (no percent, no range), else
 * "67% (44%-100%)" with the same range the capability table draws (scoreRange). "-" when nothing ran.
 */
export function rateText(m: { score: number; runs: number; ci_low?: number; ci_high?: number } | undefined, opts: { range?: boolean } = {}): string {
	if (!m || !(m.runs > 0) || !Number.isFinite(m.score)) return "-";
	if (m.runs < MIN_RUNS_TO_RATE) return `${Math.round(m.score * m.runs)} of ${m.runs}, ${TOO_FEW_TO_RATE}`;
	if (opts.range === false || m.ci_low === undefined || m.ci_high === undefined) return pct(m.score);
	const r = scoreRange({ score: m.score, runs: m.runs, ci_low: m.ci_low, ci_high: m.ci_high });
	return `${pct(m.score)} (${pct(r.low)}-${pct(r.high)})`;
}

/** Whether a rate prints as a count only (fewer than MIN_RUNS_TO_RATE runs). */
export function tooFewToRate(runs: number | undefined): boolean {
	return typeof runs === "number" && runs > 0 && runs < MIN_RUNS_TO_RATE;
}
