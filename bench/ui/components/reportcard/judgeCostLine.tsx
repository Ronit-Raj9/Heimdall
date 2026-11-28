import type { Report } from "@/lib/store/apis/evaluationsApi";
import { formatUsd } from "@/lib/money";
import { useTranslation } from "react-i18next";
import "./uff5I18n";

/** The judge part of one arm's totals (reportcard.Totals judge_*), absent on an older server. */
interface JudgeTotals {
	judge_runs?: number;
	judge_cost_usd?: number | null;
	judge_cost_runs?: number;
}

/** Judged runs and their judge cost over every arm; null when no run was judged. */
export function judgeCostOf(report: Report): { runs: number; costUsd: number | null; priced: number } | null {
	let runs = 0;
	let priced = 0;
	let cost: number | null = 0;
	for (const t of Object.values((report.totals ?? {}) as Record<string, JudgeTotals>)) {
		const n = t.judge_runs ?? 0;
		if (n === 0) continue;
		runs += n;
		priced += t.judge_cost_runs ?? 0;
		cost = cost === null || typeof t.judge_cost_usd !== "number" ? null : cost + t.judge_cost_usd;
	}
	return runs > 0 ? { runs, costUsd: cost, priced } : null;
}

/**
 * Under the cost line of a judged result (round 4, F5, PH3-2 to PH3-4): the score is judged, not verified, what
 * the judge cost apart from the agent, and that a repeat pays the judge again on the shared worker.
 */
export function JudgeCostLine({ report }: { report: Report }) {
	const { t } = useTranslation("uff5");
	const j = judgeCostOf(report);
	if (!j) return null;
	const cost =
		j.costUsd === null
			? t("judge.costUnknown", { priced: j.priced, count: j.runs })
			: t("judge.cost", { usd: formatUsd(j.costUsd), count: j.runs });
	return (
		<p data-testid="report-card-judge-cost" className="text-muted-foreground text-sm tabular-nums">
			{t("judge.judged")} {cost} {t("judge.noReuse")}
		</p>
	);
}