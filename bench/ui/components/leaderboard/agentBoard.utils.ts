import type { AgentLeaderboardRow } from "@/lib/store/apis/agentLeaderboardApi";

/** A share as whole percent, for example 0.415 -> "42%". */
export function formatShare(v: number): string {
	return `${Math.round(v * 100)}%`;
}

/** A cost per run in US dollars, "-" when never observed. */
export function formatCost(v: number | null | undefined): string {
	if (v === null || v === undefined || !Number.isFinite(v)) return "-";
	if (v === 0) return "$0";
	if (v < 0.01) return `$${v.toFixed(4)}`;
	return `$${v.toFixed(2)}`;
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
