import type { RunServicesResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * X2: GET /runs/{id}/services (the services that ran beside the agent, with the size of each
 * captured log) and GET /runs/{id}/services/{service}/log (one compose service's output, plain
 * text, served only from a sealed run that matches its seal), injected into the bench API so the
 * shared endpoint list stays untouched.
 */
export const servicesApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunServices: builder.query<RunServicesResponse, string>({
			query: (runId) => runServicesPath(runId),
		}),
		getRunServiceLog: builder.query<string, { runId: string; service: string }>({
			query: ({ runId, service }) => ({
				url: runServiceLogPath(runId, service),
				responseHandler: "text",
			}),
		}),
	}),
});

/** The request path of one run's services, with the run id escaped as one path segment. */
export function runServicesPath(runId: string): string {
	return `/runs/${encodeURIComponent(runId)}/services`;
}

/** The request path of one service's captured log. */
export function runServiceLogPath(runId: string, service: string): string {
	return `/runs/${encodeURIComponent(runId)}/services/${encodeURIComponent(service)}/log`;
}

export const { useGetRunServicesQuery, useGetRunServiceLogQuery } = servicesApi;