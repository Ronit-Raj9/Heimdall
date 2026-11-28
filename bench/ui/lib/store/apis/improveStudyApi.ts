import type { ImprovementSpecBody } from "@/lib/store/apis/improveApi";
import { improvementPath } from "@/lib/store/apis/improveApi";
import type { LineageView } from "@/lib/types/improve";
import type { StudyList, StudyView } from "@/lib/types/improvementStudy";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";
import { withOrgParam } from "./orgParam";

/** The body of POST /improvement-studies: an improvement spec with replicate.loops 2 to 5 (handlers/improvestudy.go). */
export type StudySpecBody = ImprovementSpecBody & { max_tasks?: number; replicate: { loops: number } };

/** One study's path, the id escaped as one segment. */
export function studyPath(id: string): string {
	return `/improvement-studies/${encodeURIComponent(id)}`;
}

/**
 * Improvement studies and the fresh chain check of a run's lineage (one round in full is a
 * download of GET /improvements/{id}/rounds/{k}, see roundUrl). Create and
 * stop are writes (admins); the rest are reads (viewers).
 */
export const improveStudyApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchImprovementStudy", "BenchImprovement"] }).injectEndpoints({
	endpoints: (builder) => ({
		getImprovementStudies: builder.query<StudyList, void>({
			query: () => "/improvement-studies",
			providesTags: [{ type: "BenchImprovementStudy", id: "LIST" }],
		}),
		getImprovementStudy: builder.query<StudyView, string>({
			query: (id) => studyPath(id),
			providesTags: (_r, _e, id) => [{ type: "BenchImprovementStudy", id }],
		}),
		createImprovementStudy: builder.mutation<StudyView, StudySpecBody>({
			query: (body) => ({ url: "/improvement-studies", method: "POST", body }),
			invalidatesTags: [
				{ type: "BenchImprovementStudy", id: "LIST" },
				{ type: "BenchImprovement", id: "LIST" },
			],
		}),
		stopImprovementStudy: builder.mutation<StudyView, string>({
			query: (id) => ({ url: `${studyPath(id)}/stop`, method: "POST" }),
			invalidatesTags: (_r, _e, id) => [
				{ type: "BenchImprovementStudy", id: "LIST" },
				{ type: "BenchImprovementStudy", id },
			],
		}),
		// GET /improvements/{id}/lineage: the hash chain read and checked again now.
		checkImprovementLineage: builder.query<LineageView, string>({
			query: (id) => `${improvementPath(id)}/lineage`,
		}),
	}),
});

export const {
	useGetImprovementStudiesQuery,
	useGetImprovementStudyQuery,
	useCreateImprovementStudyMutation,
	useStopImprovementStudyMutation,
	useLazyCheckImprovementLineageQuery,
} = improveStudyApi;

/** GET /improvements/{id}/rounds/{k}: one round as stored, for the Download round button. */
export function roundUrl(id: string, k: number): string {
	return withOrgParam(`${BENCH_API_BASE}${improvementPath(id)}/rounds/${k}`);
}