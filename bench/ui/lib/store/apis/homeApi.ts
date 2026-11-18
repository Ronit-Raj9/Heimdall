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
export const homeApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getExperimentEvents: builder.query<EventsPollResponse, ExperimentEventsArgs>({
			query: ({ expId, since }) => ({
				url: `/experiments/${encodeURIComponent(expId)}/events`,
				params: since > 0 ? { since: String(since) } : undefined,
			}),
			keepUnusedDataFor: 0,
		}),
	}),
});

export const { useGetExperimentEventsQuery } = homeApi;