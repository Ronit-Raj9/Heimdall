import { withOrgParam } from "./orgParam";
import type { RunResponse, VisualGrade } from "@/lib/types/bench";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";

/** X1 vision grading: one run's visual grade, read from the run detail (GET /runs/{id}). */
export interface RunVisual {
	runId: string;
	sealed: boolean;
	passed: boolean | null;
	invalid: boolean;
	reason: string;
	visual: VisualGrade | null;
}

/** The three images a visual grade keeps in the run folder. */
export type VisualImageName = "capture.png" | "reference.png" | "diff.png";

/**
 * GET /runs/{id}, narrowed to the visual grade, injected into the bench API so the shared endpoint
 * list stays untouched. A run graded without output.visual has visual null.
 */
export const visualApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunVisual: builder.query<RunVisual, string>({
			query: (runId) => `/runs/${encodeURIComponent(runId)}`,
			transformResponse: (detail: RunResponse) => toRunVisual(detail),
		}),
	}),
});

/** Narrows a run detail to its visual grade. */
export function toRunVisual(detail: RunResponse): RunVisual {
	const grade = detail.grade ?? null;
	return {
		runId: detail.run.run_id,
		sealed: detail.sealed,
		passed: grade ? grade.passed : null,
		invalid: grade?.invalid_evaluator ?? false,
		reason: grade?.reason ?? "",
		visual: grade?.visual ?? null,
	};
}

/** The URL of one image of a run's visual grade (GET /runs/{id}/visual/{name}, sealed runs only). */
export function visualImageUrl(runId: string, name: VisualImageName): string {
	return withOrgParam(`${BENCH_API_BASE}/runs/${encodeURIComponent(runId)}/visual/${name}`);
}

export const { useGetRunVisualQuery } = visualApi;