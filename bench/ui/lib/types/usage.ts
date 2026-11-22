// Usage and spend (web parity L25): GET /usage and GET /admin/usage bodies.

export type UsageGroupBy = "day" | "model" | "agent" | "key";

export interface UsageBucket {
	key: string;
	label?: string;
	cost_usd: number;
	tokens: number;
	runs: number;
	unknown_cost_runs: number;
	/** Runs that ended before any model call (no metrics, no cost rows): counted at $0. */
	no_model_call_runs?: number;
}

export interface UsageKeySpend {
	id: string;
	label: string;
	provider: string;
	last4?: string;
	budget_usd: number;
	month_spent_usd: number;
}

export interface UsageReport {
	from: string;
	to: string;
	group_by: UsageGroupBy;
	total_cost_usd: number;
	total_tokens: number;
	runs: number;
	unknown_cost_runs: number;
	/** Runs that ended before any model call, counted at $0. */
	no_model_call_runs?: number;
	experiments: number;
	buckets: UsageBucket[];
	keys: UsageKeySpend[];
	keys_month_spent_usd: number;
	note?: string;
}

export interface UsageParams {
	from?: string;
	to?: string;
	group_by?: UsageGroupBy;
}

export interface AdminOrgUsage {
	org_id: string;
	slug: string;
	name: string;
	cost_usd: number;
	tokens: number;
	runs: number;
	unknown_cost_runs: number;
	keys_month_spent_usd: number;
	error?: string;
}

export interface AdminUsageReport {
	from: string;
	to: string;
	total_cost_usd: number;
	total_tokens: number;
	runs: number;
	orgs: AdminOrgUsage[];
}

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		usage: typeof import("@/locales/en/usage.json");
	}
}