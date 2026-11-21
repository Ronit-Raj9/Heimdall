import type {
	CanaryInitRequest,
	CanaryInitResponse,
	ScheduleRunResponse,
	SchedulesFile,
	SchedulesResponse,
	TestAlertRequest,
	TestAlertResponse,
} from "@/lib/types/schedules";
import { benchApi } from "./benchApi";

/**
 * Web parity lane L17: schedules (policies/schedules.yaml), webhook alerts and canary definitions.
 * Writes carry the write token like every bench mutation.
 */
export const schedulesApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchSchedules", "BenchCanary"] }).injectEndpoints({
	endpoints: (builder) => ({
		getSchedules: builder.query<SchedulesResponse, void>({
			query: () => "/schedules",
			providesTags: ["BenchSchedules"],
		}),
		saveSchedules: builder.mutation<SchedulesResponse, { file?: SchedulesFile; yaml?: string }>({
			query: (body) => ({ url: "/schedules", method: "PUT", body }),
			invalidatesTags: ["BenchSchedules"],
		}),
		testAlert: builder.mutation<TestAlertResponse, TestAlertRequest>({
			query: (body) => ({ url: "/schedules/test-alert", method: "POST", body }),
		}),
		runSchedule: builder.mutation<ScheduleRunResponse, string>({
			query: (name) => ({ url: `/schedules/${encodeURIComponent(name)}/run`, method: "POST" }),
			invalidatesTags: ["BenchSchedules"],
		}),
		createCanary: builder.mutation<CanaryInitResponse, CanaryInitRequest>({
			query: (body) => ({ url: "/canaries", method: "POST", body }),
			invalidatesTags: [{ type: "BenchCanary", id: "LIST" }, "BenchSchedules"],
		}),
		deleteCanary: builder.mutation<void, string>({
			query: (name) => ({ url: `/canaries/${encodeURIComponent(name)}`, method: "DELETE" }),
			invalidatesTags: [{ type: "BenchCanary", id: "LIST" }, "BenchSchedules"],
		}),
	}),
});

export const {
	useGetSchedulesQuery,
	useSaveSchedulesMutation,
	useTestAlertMutation,
	useRunScheduleMutation,
	useCreateCanaryMutation,
	useDeleteCanaryMutation,
} = schedulesApi;

/** The server's error message from an RTK Query error, or a fallback. */
export function errorMessage(err: unknown, fallback: string): string {
	if (err && typeof err === "object" && "data" in err) {
		const data = (err as { data?: unknown }).data;
		if (data && typeof data === "object") {
			const d = data as { error?: { message?: string } | string; message?: string };
			if (typeof d.error === "string") return d.error;
			if (d.error && typeof d.error.message === "string") return d.error.message;
			if (typeof d.message === "string") return d.message;
		}
	}
	return fallback;
}
