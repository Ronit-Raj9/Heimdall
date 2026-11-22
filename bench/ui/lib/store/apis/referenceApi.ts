import { benchApi } from "./benchApi";

/**
 * Reference board (gap G4-03, bench/handlers/reference.go): scores of known agents on the built-in
 * benchmarks, from real runs the operator published. Public and read-only. Types mirror the Go JSON.
 */
export interface ReferenceEntry {
	id: string;
	evaluation_id: string;
	experiment_id: string;
	benchmark: string;
	benchmark_version?: string;
	lock_hash?: string;
	capability: string;
	agent: string;
	/** Saved agent version in the reference organization; absent for runs imported from elsewhere. */
	version?: number;
	/** The build the agent config declares (agent_version). */
	agent_build?: string;
	model: string;
	score: number;
	ci_low: number;
	ci_high: number;
	runs: number;
	tasks: number;
	solved: number;
	/** Null unless every run's cost is known. */
	cost_usd: number | null;
	/** Null when the cost is unknown or nothing was solved. */
	cost_per_solved_usd: number | null;
	wall_s: number;
	ran_at: string;
	/** Fingerprint of the verified runs (the last seal of the experiment's chain). */
	seal_hash: string;
	sealed_runs: number;
	/** Path of the read-only report card page (/s/<token>). */
	report_path?: string;
	published_at: string;
	published_by?: string;
}

export interface ReferenceBoard {
	org: string;
	benchmark: string;
	benchmarks: string[];
	entries: ReferenceEntry[];
	updated_at?: string;
}

export const referenceApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getReferenceBoard: builder.query<ReferenceBoard, { benchmark?: string } | void>({
			query: (args) => (args && args.benchmark ? { url: "/reference/board", params: { benchmark: args.benchmark } } : "/reference/board"),
		}),
	}),
});

export const { useGetReferenceBoardQuery } = referenceApi;
