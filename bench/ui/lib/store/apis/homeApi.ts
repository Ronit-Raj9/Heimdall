import type { EventsPollResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

export interface ExperimentEventsArgs {
	expId: string;
	/** The `next` of the previous poll; 0 for the first. */
	since: number;
}

/**
 * GET /experiments/{id}/events in its JSON form (no text/event-stream Accept header): the events
 * after ?since= plus the experiment's status. Home polls it for each running experiment, which
 * works through proxies that buffer streams and in fixture mode. Injected so the shared endpoint
 * list stays untouched.
 */
/**
 * GET /home/summary (Q-112): Home's tiles in one call. counts holds the list totals by name; the
 * latest lists are the rows the evaluations and comparisons lists answer. Every field but counts
 * may be missing (older servers, or a part that failed): keep today's queries as the fallback.
 */
export interface HomeSummary {
	counts: Record<string, number>;
	latest_evaluations?: unknown[];
	latest_comparisons?: unknown[];
	running?: number;
	spend_month_usd?: number;
	spend_month_unknown_cost_runs?: number;
	spend_month_from?: string;
}

export const homeApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getHomeSummary: builder.query<HomeSummary, void>({
			query: () => "/home/summary",
		}),
		getExperimentEvents: builder.query<EventsPollResponse, ExperimentEventsArgs>({
			query: ({ expId, since }) => ({
				url: `/experiments/${encodeURIComponent(expId)}/events`,
				params: since > 0 ? { since: String(since) } : undefined,
			}),
			keepUnusedDataFor: 0,
		}),
	}),
});

export const { useGetExperimentEventsQuery, useGetHomeSummaryQuery } = homeApi;