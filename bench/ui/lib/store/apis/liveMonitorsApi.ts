import type { FetchArgs } from "@reduxjs/toolkit/query/react";
import { benchApi } from "./benchApi";

/**
 * Lane Z22 live monitors (handlers/livemonitors.go): a few ready tasks run against a deployed
 * agent on a schedule, with pass rate, latency, cost and errors over time and one incident per
 * drop or outage. Injected into the bench API so the shared endpoint list stays untouched.
 */

export interface MonitorRule {
	window?: number;
	baseline?: number;
	alpha?: number;
	min_drop?: number;
	floor?: number;
	[key: string]: unknown;
}

export interface LiveMonitor {
	id: string;
	name: string;
	agent: string;
	version?: number;
	model?: string;
	provider?: string;
	benchmark: string;
	tasks: number;
	interval_min: number;
	budget_usd_per_day: number;
	worker?: string;
	paused: boolean;
	rule: MonitorRule;
	badge: boolean;
	created_at: string;
	updated_at: string;
}

export interface MonitorRunSample {
	run_id: string;
	task: string;
	outcome: string;
	error?: string;
	[key: string]: unknown;
}

export interface MonitorSample {
	at: string;
	evaluation_id?: string;
	experiment_id?: string;
	status: string;
	reason?: string;
	runs: MonitorRunSample[];
	passed: number;
	scored: number;
	errors: number;
	p50_ms?: number;
	p95_ms?: number;
	cost_usd: number;
	cost_unknown?: number;
	reachable?: boolean;
	incident?: string;
}

export interface MonitorIncident {
	id: string;
	monitor: string;
	kind: string;
	state: string;
	title: string;
	detail: string;
	opened_at: string;
	updated_at: string;
	resolved_at?: string;
	p_value?: number;
	checks: number;
	failing_runs: { run_id: string; task: string; outcome: string; error?: string }[];
	evaluations: string[];
}

export interface MonitorStats {
	checks: number;
	ok_checks: number;
	infra_checks: number;
	passed: number;
	scored: number;
	pass_rate: number | null;
	pass_low: number | null;
	pass_high: number | null;
	p50_ms: number | null;
	p95_ms: number | null;
	cost_usd: number;
	cost_unknown: number;
	errors: number;
	uptime: number | null;
}

export interface MonitorRow extends LiveMonitor {
	status: string;
	schedule: string;
	cron: string;
	next?: string;
	check_budget_usd: number;
	spent_today_usd: number;
	last?: MonitorSample;
	day: MonitorStats;
	week: MonitorStats;
	open_incidents: MonitorIncident[];
	badge_path?: string;
	problem?: string;
}

export interface MonitorListResponse {
	monitors: MonitorRow[];
	intervals: number[];
	max_tasks: number;
	min_interval_min: number;
	ticker: boolean;
}

/** The detail's tasks are the task ids each check runs (they shadow the row's task count). */
export interface MonitorDetailResponse extends Omit<MonitorRow, "tasks"> {
	samples: MonitorSample[];
	incidents: MonitorIncident[];
	tasks: string[];
	tasks_note?: string;
}

export interface MonitorInput {
	name?: string;
	agent?: string;
	benchmark?: string;
	tasks?: number;
	interval_min?: number;
	budget_usd_per_day?: number;
}

const LIST = { type: "BenchLiveMonitor" as const, id: "LIST" };

export const liveMonitorsApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchLiveMonitor"] }).injectEndpoints({
	endpoints: (builder) => ({
		getLiveMonitors: builder.query<MonitorListResponse, void>({
			query: () => "/live-monitors",
			providesTags: [LIST],
		}),
		getLiveMonitor: builder.query<MonitorDetailResponse, string>({
			query: (id) => monitorPath(id),
			providesTags: (_r, _e, id) => [{ type: "BenchLiveMonitor", id }],
		}),
		createLiveMonitor: builder.mutation<MonitorDetailResponse, MonitorInput>({
			query: (body) => ({ url: "/live-monitors", method: "POST", body }),
			invalidatesTags: [LIST],
		}),
		monitorAction: builder.mutation<unknown, { id: string; action: "pause" | "resume" | "run" | "delete" }>({
			query: ({ id, action }) => monitorActionRequest(id, action),
			invalidatesTags: (_r, _e, { id }) => [LIST, { type: "BenchLiveMonitor", id }],
		}),
		setMonitorBadge: builder.mutation<unknown, { id: string; enabled: boolean }>({
			query: ({ id, enabled }) => ({ url: `${monitorPath(id)}/badge`, method: "POST", body: { enabled } }),
			invalidatesTags: (_r, _e, { id }) => [LIST, { type: "BenchLiveMonitor", id }],
		}),
	}),
});

export function monitorPath(id: string): string {
	return `/live-monitors/${encodeURIComponent(id)}`;
}

export function monitorActionRequest(id: string, action: "pause" | "resume" | "run" | "delete"): FetchArgs {
	if (action === "delete") return { url: monitorPath(id), method: "DELETE" };
	return { url: `${monitorPath(id)}/${action}`, method: "POST" };
}

export const {
	useGetLiveMonitorsQuery,
	useGetLiveMonitorQuery,
	useCreateLiveMonitorMutation,
	useMonitorActionMutation,
	useSetMonitorBadgeMutation,
} = liveMonitorsApi;
