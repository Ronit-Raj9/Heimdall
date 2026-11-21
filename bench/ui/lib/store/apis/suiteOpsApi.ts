import type { QuarantineRequest, QuarantineResult } from "@/lib/types/suiteops";
import { benchApi } from "./benchApi";

const seg = (v: string) => encodeURIComponent(v);

/** Suite ops area (web parity lane L11): quarantine and restore one task. */
export const suiteOpsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		quarantineTask: builder.mutation<QuarantineResult, QuarantineRequest>({
			query: ({ suite, task, reason }) => ({
				url: `/suites/${seg(suite)}/tasks/${seg(task)}/quarantine`,
				method: "POST",
				body: { reason },
			}),
			invalidatesTags: (_r, _e, { suite, task }) => [
				{ type: "BenchSuites", id: "LIST" },
				{ type: "BenchSuites", id: `${suite}/health` },
				{ type: "BenchSuites", id: `${suite}/${task}` },
			],
		}),
		restoreTask: builder.mutation<QuarantineResult, { suite: string; task: string }>({
			query: ({ suite, task }) => ({ url: `/suites/${seg(suite)}/tasks/${seg(task)}/restore`, method: "POST" }),
			invalidatesTags: (_r, _e, { suite, task }) => [
				{ type: "BenchSuites", id: "LIST" },
				{ type: "BenchSuites", id: `${suite}/health` },
				{ type: "BenchSuites", id: `${suite}/${task}` },
			],
		}),
	}),
});

export const { useQuarantineTaskMutation, useRestoreTaskMutation } = suiteOpsApi;
