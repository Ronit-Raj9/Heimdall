import { transportErrorText } from "@/lib/requestErrors";
import type { CanaryDefinition } from "@/lib/types/bench";
import type {
	CanaryInitRequest,
	CanaryInitResponse,
	ScheduleRunResponse,
	SchedulesFile,
	SchedulesResponse,
	TestAlertRequest,
	TestAlertResponse,
	Schedule,
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
		// Create, edit and delete also drop the cached detail of that name, so a drift monitor deleted
		// and created again shows its new definition at once.
		createCanary: builder.mutation<CanaryInitResponse, CanaryInitRequest>({
			query: (body) => ({ url: "/canaries", method: "POST", body }),
			invalidatesTags: (_r, _e, body) => canaryTags(body.name),
		}),
		editCanary: builder.mutation<CanaryDefinition, CanaryInitRequest>({
			query: (body) => ({ url: `/canaries/${encodeURIComponent(body.name)}`, method: "PUT", body }),
			invalidatesTags: (_r, _e, body) => canaryTags(body.name),
		}),
		deleteCanary: builder.mutation<void, string>({
			query: (name) => ({ url: `/canaries/${encodeURIComponent(name)}`, method: "DELETE" }),
			invalidatesTags: (_r, _e, name) => canaryTags(name),
		}),
		setCanaryPaused: builder.mutation<CanaryDefinition, { name: string; paused: boolean }>({
			query: ({ name, paused }) => ({ url: `/canaries/${encodeURIComponent(name)}/${paused ? "pause" : "resume"}`, method: "POST" }),
			invalidatesTags: (_r, _e, { name }) => canaryTags(name),
		}),
		setSchedulePaused: builder.mutation<Schedule, { name: string; paused: boolean }>({
			query: ({ name, paused }) => ({ url: `/schedules/${encodeURIComponent(name)}/${paused ? "pause" : "resume"}`, method: "POST" }),
			invalidatesTags: (_r, _e, { name }) => (name.startsWith("canary-") ? canaryTags(name.slice("canary-".length)) : ["BenchSchedules"]),
		}),
	}),
});

export const {
	useGetSchedulesQuery,
	useSaveSchedulesMutation,
	useTestAlertMutation,
	useRunScheduleMutation,
	useCreateCanaryMutation,
	useEditCanaryMutation,
	useDeleteCanaryMutation,
	useSetCanaryPausedMutation,
	useSetSchedulePausedMutation,
} = schedulesApi;

/** The tags a change to drift monitor name invalidates: the list, its detail and the schedules. */
export function canaryTags(name: string) {
	return [{ type: "BenchCanary" as const, id: "LIST" }, { type: "BenchCanary" as const, id: name }, "BenchSchedules" as const];
}

/** A schedule or a drift monitor schedule with its paused flag (F17; not yet in the shared types). */
export type Pausable = { paused?: boolean };

/** Whether a schedule row or a drift monitor schedule is paused. */
export function isPaused(x: unknown): boolean {
	return Boolean(x && typeof x === "object" && (x as Pausable).paused);
}

/** One problem of a refused drift monitor (POST and PUT /canaries answer 422 with problems[]). */
export interface FieldProblem {
	field: string;
	message: string;
}

/** The problems[] of a refused drift monitor, each under its form field; [] for any other error. */
export function errorProblems(err: unknown): FieldProblem[] {
	if (!err || typeof err !== "object" || !("data" in err)) return [];
	const data = (err as { data?: unknown }).data;
	if (!data || typeof data !== "object") return [];
	const list = (data as { problems?: unknown }).problems;
	if (!Array.isArray(list)) return [];
	return list.filter(
		(p): p is FieldProblem =>
			Boolean(p) && typeof (p as FieldProblem).field === "string" && typeof (p as FieldProblem).message === "string",
	);
}

/** The server's error message from an RTK Query error, or a fallback. */
export function errorMessage(err: unknown, fallback: string): string {
	const transport = transportErrorText(err);
	if (transport) return transport;
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
/** The server's schedule error without its leading "schedule <name>: ", which the form already shows. */
export function plainScheduleError(msg: string): string {
	return msg.replace(/^schedule [^:]*: /, "");
}