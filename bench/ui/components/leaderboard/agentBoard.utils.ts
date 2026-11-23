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