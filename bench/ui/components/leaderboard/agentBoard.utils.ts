import type { AgentLeaderboardRow } from "@/lib/store/apis/agentLeaderboardApi";
import { formatUsd } from "@/lib/money";

/** A share as whole percent, for example 0.415 -> "42%". */
export function formatShare(v: number): string {
	return `${Math.round(v * 100)}%`;
}

/** Ranking needs this many runs and tasks unless the server says otherwise. */
export const DEFAULT_MIN_RUNS = 10;
export const DEFAULT_MIN_TASKS = 5;

/** The row's cost display: the cost per run, or not known with how many runs are unpriced and why. */
export function rowCost(row: AgentLeaderboardRow): { known: boolean; text: string; unpriced: number; runs: number; note: string } {
	const runs = row.runs ?? 0;
	if (row.cost_usd !== null && row.cost_usd !== undefined && Number.isFinite(row.cost_usd)) {
		return { known: true, text: formatCost(row.cost_usd), unpriced: 0, runs, note: "" };
	}
	return { known: false, text: "", unpriced: Math.max(0, runs - (row.cost_runs ?? 0)), runs, note: row.cost_note ?? "" };
}

/** A cost per run in US dollars, "-" when never observed. */
export function formatCost(v: number | null | undefined): string {
	return formatUsd(v);
}

/** The search of the agent page's report card for a row: version only when saved. */
export function reportCardSearch(row: AgentLeaderboardRow): { tab: string; version?: string; model?: string } {
	return {
		tab: "report-card",
		...(row.version > 0 ? { version: String(row.version) } : {}),
		...(row.model ? { model: row.model } : {}),
	};
}

/** A stable React key for a row. */
export function rowKey(row: AgentLeaderboardRow): string {
	return [row.agent, row.version, row.model, row.config_hash ?? "", row.simulator ?? ""].join("|");
}

/** The value a filter select shows for "no filter". Never a real capability or model. */
export const ALL = "~all";

/** A filter select's value as the query param: absent for ALL. */
export function filterParam(value: string | null | undefined): string | undefined {
	return value && value !== ALL ? value : undefined;
}
/** How a capability cell reads: no runs at all, too few to rank, or a ranked score. */
export type ScoreState = "notMeasured" | "tooFew" | "ranked";

/** A score with no runs is "not measured" and a score under the minimum sample is "too few runs": neither prints a percent. */
export function scoreState(s: { runs: number; ranked?: boolean } | undefined): ScoreState {
	if (!s || !(s.runs > 0)) return "notMeasured";
	return s.ranked === false ? "tooFew" : "ranked";
}

/** A 95% range is worth printing only when its ends differ once rounded to whole percent. */
export function showRange(low: number, high: number): boolean {
	return formatShare(low) !== formatShare(high);
}

/** A cost per run with 2 significant digits, for example 0.00234 -> "$0.0023", 0.1234 -> "$0.12", 3.2 -> "$3.20". */
export function formatCostPerRun(v: number | null | undefined): string {
	if (v === null || v === undefined || !Number.isFinite(v)) return "-";
	if (v === 0) return "$0";
	const a = Math.abs(v);
	const sign = v < 0 ? "-" : "";
	if (a >= 10) return `${sign}$${Math.round(a).toLocaleString("en-US")}`;
	if (a >= 1) return `${sign}$${a.toFixed(2)}`;
	const digits = Math.max(2, 1 - Math.floor(Math.log10(a)));
	return `${sign}$${a.toFixed(digits)}`;
}

/** The score a row is ranked by: the filtered capability, or the run-weighted share over its ranked capabilities (the All score). */
export function rankScore(
	row: AgentLeaderboardRow,
	capability?: string,
): { score: number; low: number; high: number } | null {
	const list = capability ? [row.capabilities[capability]] : Object.values(row.capabilities);
	const ranked = list.filter((s): s is NonNullable<typeof s> => scoreState(s) === "ranked");
	if (ranked.length === 0) return null;
	const runs = ranked.reduce((n, s) => n + s.runs, 0);
	const avg = (pick: (s: (typeof ranked)[number]) => number) => ranked.reduce((n, s) => n + pick(s) * s.runs, 0) / runs;
	return { score: avg((s) => s.score), low: avg((s) => s.ci_low), high: avg((s) => s.ci_high) };
}

/** A row's place: its number, whether its range overlaps the row above (shown "=n"), and whether it is a clear best. */
export interface BoardRank {
	rank: number;
	tied: boolean;
	best: boolean;
}

/**
 * Ranks every row with a ranked score, best score first, ties in score ordered by lower cost.
 * A row whose range overlaps the row above shares that row's number ("=n"). Row 1 is "best" only
 * when its range does not overlap row 2. Rows with no ranked score get no rank.
 */
export function boardRanks(rows: readonly AgentLeaderboardRow[], capability?: string): Map<string, BoardRank> {
	const scored = rows
		.map((row) => ({ row, s: rankScore(row, capability) }))
		.filter((x): x is { row: AgentLeaderboardRow; s: NonNullable<ReturnType<typeof rankScore>> } => x.s !== null)
		.sort((a, b) => b.s.score - a.s.score || (a.row.cost_usd ?? Infinity) - (b.row.cost_usd ?? Infinity));
	const out = new Map<string, BoardRank>();
	scored.forEach((x, i) => {
		const above = scored[i - 1];
		const tied = above !== undefined && x.s.high >= above.s.low;
		const rank = tied ? (out.get(rowKey(above.row))?.rank ?? i + 1) : i + 1;
		const next = scored[i + 1];
		out.set(rowKey(x.row), { rank, tied, best: i === 0 && (next === undefined || next.s.high < x.s.low) && scored.length > 1 });
	});
	return out;
}

/** True when every row is the same agent, so the model is the part worth reading first. */
export function sameAgent(rows: readonly AgentLeaderboardRow[]): boolean {
	return rows.length > 1 && rows.every((r) => r.agent === rows[0].agent);
}
