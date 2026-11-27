import { formatUsd } from "@/lib/money";

/**
 * The three amounts every repeat run text shows (R6-3), worked out and formatted in one place so a
 * form, its button and a confirm never round the same number two ways:
 * - perRun: the expected cost of one run (what the last run cost), or null when not known;
 * - dailyLimit: the most it spends in one day, or null when no limit is set;
 * - monthMax: the most it can spend in a month, 30 days at the daily limit, or null without a limit.
 * runsADay is how many runs the limit lets start each day (never more than the timetable asks).
 * Every amount is formatted with formatUsd, the format the paid buttons use. Pure.
 */
export interface RepeatCost {
	perRun: string | null;
	dailyLimit: string | null;
	monthMax: string | null;
	runsADay: number | null;
}

export const DAYS_A_MONTH = 30;

export function repeatCost(perRunUsd: number | null | undefined, dailyLimitUsd: number | null | undefined, timetableRunsADay?: number): RepeatCost {
	const perRun = perRunUsd != null && perRunUsd > 0 ? perRunUsd : null;
	const limit = dailyLimitUsd != null && dailyLimitUsd > 0 ? dailyLimitUsd : null;
	let runsADay: number | null = null;
	if (perRun !== null && limit !== null) {
		// A small tolerance, so a limit of exactly two runs allows two runs despite floating point.
		const fit = Math.floor(limit / perRun + 1e-9);
		runsADay = timetableRunsADay != null && timetableRunsADay > 0 ? Math.min(Math.ceil(timetableRunsADay), fit) : fit;
	}
	return {
		perRun: perRun === null ? null : formatUsd(perRun),
		dailyLimit: limit === null ? null : formatUsd(limit),
		monthMax: limit === null ? null : formatUsd(limit * DAYS_A_MONTH),
		runsADay,
	};
}

/** The keys and values of the cost sentence (ufrj namespace, repeat.cost.*). Pure. */
export function repeatCostText(c: RepeatCost): { key: "repeat.cost.known" | "repeat.cost.unknownRun" | "repeat.cost.noLimit" | "repeat.cost.none"; values: Record<string, string | number> } {
	if (c.dailyLimit === null) return c.perRun === null ? { key: "repeat.cost.none", values: {} } : { key: "repeat.cost.noLimit", values: { perRun: c.perRun } };
	if (c.perRun === null) return { key: "repeat.cost.unknownRun", values: { limit: c.dailyLimit, month: c.monthMax ?? "" } };
	return { key: "repeat.cost.known", values: { perRun: c.perRun, limit: c.dailyLimit, month: c.monthMax ?? "", count: c.runsADay ?? 0 } };
}
