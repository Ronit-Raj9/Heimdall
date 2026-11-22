import type { ScenarioRowsRequest, ScenarioRowsResult, SuiteMockRequest, SuiteMockResult } from "@/lib/types/scenarioBuild";
import { benchApi } from "./benchApi";

const seg = (v: string) => encodeURIComponent(v);
const suiteTags = (suite: string) => [
	{ type: "BenchSuites" as const, id: "LIST" },
	{ type: "BenchSuites" as const, id: `${suite}/health` },
];

/** Scenario rows and OpenAPI mocks (lane Y14). A dry run changes nothing, so it refreshes nothing. */
export const scenarioBuildApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		importScenarioRows: builder.mutation<ScenarioRowsResult, { suite: string } & ScenarioRowsRequest>({
			query: ({ suite, ...body }) => ({ url: `/suites/${seg(suite)}/scenario-rows`, method: "POST", body }),
			invalidatesTags: (r, _e, { suite, dry_run }) => (dry_run || !r ? [] : suiteTags(suite)),
		}),
		writeSuiteMock: builder.mutation<SuiteMockResult, { suite: string } & SuiteMockRequest>({
			query: ({ suite, ...body }) => ({ url: `/suites/${seg(suite)}/mocks`, method: "POST", body }),
			invalidatesTags: (r, _e, { suite, dry_run }) => (dry_run || !r ? [] : suiteTags(suite)),
		}),
	}),
});

export const { useImportScenarioRowsMutation, useWriteSuiteMockMutation } = scenarioBuildApi;
