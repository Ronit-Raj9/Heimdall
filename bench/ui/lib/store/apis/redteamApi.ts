import type { FetchArgs } from "@reduxjs/toolkit/query/react";
import { benchApi } from "./benchApi";

/**
 * Z04 red team (docs/red-team.md), injected into the bench API. Writing the benchmark refreshes
 * the suite list so the wizard sees it; a review refreshes the regressions list.
 */
export interface RedTeamOption {
	id: string;
	label: string;
	description: string;
	channels?: string[];
	source?: string;
}

export interface RedTeamCatalog {
	categories: RedTeamOption[];
	strategies: RedTeamOption[];
	channels: RedTeamOption[];
	suite: string;
	max_attempts: number;
	regressions: string;
}

export interface RedTeamPlan {
	suite?: string;
	categories: string[];
	strategies: string[];
	channels: string[];
	max_attempts?: number;
	attacker_provider: string;
	attacker_model: string;
	replace?: boolean;
}

export interface RedTeamBenchmarkResult {
	suite: string;
	tasks: string[];
	files: number;
	replaced: boolean;
	next: string;
}

export interface RedTeamReviewItem {
	task: string;
	state: "pending" | "accepted" | "rejected" | string;
	category: string;
	strategy: string;
	channel: string;
	evaluation?: string;
	run: string;
	source_suite: string;
	source_task: string;
	attempts_to_break?: number;
	attacker_model?: string;
	payload_sha256: string;
	created: string;
	decided_by?: string;
}

export interface RedTeamRegressions {
	suite: string;
	exists: boolean;
	items: RedTeamReviewItem[];
	pending: number;
}

export const redteamApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchRedTeam"] }).injectEndpoints({
	endpoints: (builder) => ({
		getRedTeamCatalog: builder.query<RedTeamCatalog, void>({ query: () => "/redteam/catalog" }),
		createRedTeamBenchmark: builder.mutation<RedTeamBenchmarkResult, RedTeamPlan>({
			query: (plan) => redTeamBenchmarkRequest(plan),
			invalidatesTags: [{ type: "BenchSuites", id: "LIST" }],
		}),
		getRedTeamRegressions: builder.query<RedTeamRegressions, void>({
			query: () => "/redteam/regressions",
			providesTags: [{ type: "BenchRedTeam", id: "REGRESSIONS" }],
		}),
		reviewRedTeamRegression: builder.mutation<RedTeamReviewItem, { task: string; decision: "accepted" | "rejected" }>({
			query: ({ task, decision }) => ({ url: `/redteam/regressions/${encodeURIComponent(task)}/review`, method: "POST", body: { decision } }),
			invalidatesTags: [
				{ type: "BenchRedTeam", id: "REGRESSIONS" },
				{ type: "BenchSuites", id: "LIST" },
			],
		}),
	}),
});

/** POST /redteam/benchmark. */
export function redTeamBenchmarkRequest(plan: RedTeamPlan): FetchArgs {
	return { url: "/redteam/benchmark", method: "POST", body: plan };
}

export const { useGetRedTeamCatalogQuery, useCreateRedTeamBenchmarkMutation, useGetRedTeamRegressionsQuery, useReviewRedTeamRegressionMutation } = redteamApi;
