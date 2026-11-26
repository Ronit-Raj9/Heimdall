import type { InboxGuide, InboxIngestResult } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/** One quote the model cited: run_id is the inbox item, seq the tool call (0 for the turns and the final answer). */
export interface ProposalQuote {
	run_id: string;
	seq: number;
	text: string;
}

/** Why the model suggested one guide field, with the quotes that were found in the session. */
export interface ProposalReason {
	field: "answers" | "calls" | "forbidden" | "reply_rubric" | "instruction";
	text: string;
	quotes: ProposalQuote[];
}

/** GET /inbox/{id}/proposal: the model's draft of what should have happened (wave 4, trace to environment). */
export interface TraceProposal {
	item: string;
	kind: string;
	guide: InboxGuide;
	reasons: ProposalReason[];
	origin: string;
	model: string;
	cost_usd: number;
	dropped_unverified: number;
	created_at: string;
}

/** The session file upload and the model's draft of an inbox item. */
export const traceDraftApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchInbox", "TraceProposal"] }).injectEndpoints({
	endpoints: (builder) => ({
		importInboxSession: builder.mutation<InboxIngestResult, File>({
			query: (file) => ({
				url: "/inbox/import-session",
				method: "POST",
				body: file,
				headers: { "Content-Type": "application/octet-stream", "X-File-Name": file.name },
			}),
			invalidatesTags: [{ type: "BenchInbox", id: "LIST" }],
		}),
		getInboxProposal: builder.query<TraceProposal, string>({
			query: (itemId) => `/inbox/${encodeURIComponent(itemId)}/proposal`,
			providesTags: (_result, _error, itemId) => [{ type: "TraceProposal", id: itemId }],
		}),
	}),
});

export const { useImportInboxSessionMutation, useGetInboxProposalQuery, useLazyGetInboxProposalQuery } = traceDraftApi;