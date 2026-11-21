import type { QuickRunRequest, QuickRunResponse } from "@/lib/types/quickrun";
import { benchApi } from "./benchApi";

/** POST /quickrun: writes experiments/quick-<ts>.yaml, plans, estimates, creates and starts it (lane L28). */
export const quickrunApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		quickRun: builder.mutation<QuickRunResponse, QuickRunRequest>({
			query: (body) => ({ url: "/quickrun", method: "POST", body }),
			invalidatesTags: [{ type: "BenchExperiment", id: "LIST" }],
		}),
	}),
});

export const { useQuickRunMutation } = quickrunApi;
