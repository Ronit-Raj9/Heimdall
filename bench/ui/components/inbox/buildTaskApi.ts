import { benchApi } from "@/lib/store/apis/benchApi";

// Round 7 lane T3: the trace builder routes (handlers/tracetask_t3.go). Shapes follow the Go types.

export interface BuildTaskQuote {
	text: string;
	turn?: number;
	role?: string;
}
export interface BuildTaskFile {
	path: string;
	content: string;
}
export interface BuildTaskCheck {
	name: string;
	want?: string;
	got: string;
	detail?: string;
}
export interface BuildTaskResult {
	failure: { kind: string; summary: string; quotes: BuildTaskQuote[]; dropped_quotes?: number };
	shape: string;
	shape_reason: string;
	files: BuildTaskFile[];
	mutants: { name: string; what_it_does_wrong: string }[];
	standin: { why: string; files: BuildTaskFile[] } | null;
	spent_usd: number;
	model_calls: number;
	notes?: string[];
	unsure?: boolean;
	task?: { suite: string; id: string; path: string };
	admission?: { admitted: boolean; controls: BuildTaskCheck[]; guards: BuildTaskCheck[] };
}
export interface BuildTaskDecision {
	job_id: string;
	decision: "approved" | "rejected";
	by: string;
	at: string;
	task?: string;
}
export interface BuildTaskState {
	job_id?: string;
	status?: string;
	step?: string;
	error?: string;
	result?: BuildTaskResult;
	decision?: BuildTaskDecision;
	can_approve: boolean;
	blocked?: string;
	can_retry: boolean;
	failing?: string[];
	budget_usd?: number;
}
export interface BuildTaskBody {
	suite: string;
	model: string;
	budget_usd: number;
	retry?: boolean;
}

/** The body POST /inbox/{id}/build-task takes from the form. */
export function buildTaskBody(suite: string, model: string, cap: number): BuildTaskBody {
	return { suite, model, budget_usd: cap };
}

const path = (item: string) => `/inbox/${encodeURIComponent(item)}/build-task`;

export const buildTaskApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getBuildTask: builder.query<BuildTaskState, string>({
			query: (item) => ({ url: path(item) }),
		}),
		startBuildTask: builder.mutation<{ job_id: string }, { item: string; body: BuildTaskBody }>({
			query: ({ item, body }) => ({ url: path(item), method: "POST", body }),
		}),
	}),
	overrideExisting: false,
});

export const { useGetBuildTaskQuery, useStartBuildTaskMutation } = buildTaskApi;
