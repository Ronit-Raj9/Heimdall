import type {
	DebugMode,
	DebugRunRequest,
	DebugRunResponse,
	HoldRelease,
	HoldStatus,
	RunCallDetail,
	RunCalls,
} from "@/lib/types/runDebug";
import { benchApi } from "./benchApi";

const runPath = (runId: string) => `/runs/${encodeURIComponent(runId)}`;

/** Route of each debug run mode: replay, fork, or a live rerun with breakpoints. */
export const DEBUG_ROUTE: Record<DebugMode, string> = { replay: "replay", fork: "fork", step: "debug" };

/**
 * X20b run debugging (handlers/rundebug.go): model calls with their raw bodies, debug runs
 * (replay, fork, breakpoints) and the release of a held call.
 */
export const runDebugApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunCalls: builder.query<RunCalls, string>({
			query: (runId) => `${runPath(runId)}/calls`,
		}),
		getRunCall: builder.query<RunCallDetail, { runId: string; requestId: string }>({
			query: ({ runId, requestId }) => `${runPath(runId)}/calls/${encodeURIComponent(requestId)}`,
		}),
		getRunHolds: builder.query<HoldStatus, string>({
			query: (runId) => `${runPath(runId)}/holds`,
		}),
		startDebugRun: builder.mutation<DebugRunResponse, { runId: string; mode: DebugMode; body: DebugRunRequest }>({
			query: ({ runId, mode, body }) => ({ url: `${runPath(runId)}/${DEBUG_ROUTE[mode]}`, method: "POST", body }),
			invalidatesTags: [{ type: "BenchExperiment", id: "LIST" }],
		}),
		releaseHold: builder.mutation<void, { runId: string; body: HoldRelease }>({
			query: ({ runId, body }) => ({ url: `${runPath(runId)}/holds/release`, method: "POST", body }),
		}),
	}),
});

export const {
	useGetRunCallsQuery,
	useGetRunCallQuery,
	useGetRunHoldsQuery,
	useStartDebugRunMutation,
	useReleaseHoldMutation,
} = runDebugApi;
