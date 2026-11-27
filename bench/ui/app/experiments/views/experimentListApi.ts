import { benchApi } from "@/lib/store/apis/benchApi";
import type { ExperimentListResponse, ExperimentStatus } from "@/lib/types/bench";

/**
 * One setup's raw numbers from `GET /experiments?arm_stats=1` (G3, M4-66). Optional: a server
 * without the parameter sends no `arm_stats`, and the list then reads as before.
 */
export interface ArmStat {
	arm: string;
	runs: number;
	passed: number;
	/** Null with no run. */
	pass_rate: number | null;
	/** Null unless every counted run has an observed cost (an unpriced run is never $0). */
	cost_usd: number | null;
	cost_runs: number;
}

/** A comparisons list row with the optional per-setup numbers. */
export type ExperimentRow = ExperimentStatus & { arm_stats?: ArmStat[] };

/**
 * The comparisons list (/experiments) with per-setup numbers. It shares the LIST tag with
 * `GET /experiments`, so every create, start or delete refreshes it too. The whole list comes
 * back (no limit): search and filters run on the page, and the Pager pages the result.
 */
export const experimentListApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		listExperimentsWithStats: builder.query<ExperimentListResponse & { experiments: ExperimentRow[] }, void>({
			query: () => "/experiments?arm_stats=1",
			providesTags: [{ type: "BenchExperiment", id: "LIST" }],
		}),
	}),
});

export const { useListExperimentsWithStatsQuery } = experimentListApi;
