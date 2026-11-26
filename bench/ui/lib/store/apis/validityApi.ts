import { benchApi } from "./benchApi";

/** One side of a re-score: its grade source and verdict (validity/validity.go Side). */
export interface ValiditySide {
	source: string;
	decision: string;
	action?: string;
	invalid_reasons?: string[];
}

/** One re-score of one comparison (validity/validity.go Entry). */
export interface ValidityEntry {
	id: string;
	exp_id: string;
	regrade_id: string;
	note?: string;
	gates?: Record<string, string>;
	before: ValiditySide;
	after: ValiditySide;
	flipped: boolean;
	at: string;
	seen_by?: string[];
}

/** A comparison a check update could change (validity/validity.go Stale). */
export interface ValidityStale {
	exp_id: string;
	gates: Record<string, [string, string]>;
	checked: string;
}

export interface ValidityList {
	entries: ValidityEntry[];
	unseen: number;
	stale: ValidityStale[];
}

export interface ExperimentValidity {
	entries: ValidityEntry[];
	stale: ValidityStale | null;
}

/** W4-49: the validity ledger routes (handlers/validity_w449.go). */
export const validityApi = benchApi.enhanceEndpoints({ addTagTypes: ["Validity"] }).injectEndpoints({
	endpoints: (builder) => ({
		getValidity: builder.query<ValidityList, { flipped?: boolean } | void>({
			query: (arg) => ({ url: "/validity", params: arg && arg.flipped ? { flipped: "1" } : undefined }),
			providesTags: ["Validity"],
		}),
		getExperimentValidity: builder.query<ExperimentValidity, string>({
			query: (id) => `/experiments/${encodeURIComponent(id)}/validity`,
			providesTags: ["Validity"],
		}),
		markValiditySeen: builder.mutation<void, string>({
			query: (id) => ({ url: `/validity/${encodeURIComponent(id)}/seen`, method: "POST" }),
			invalidatesTags: (_r, error) => (error ? [] : ["Validity"]),
		}),
	}),
});

export const { useGetValidityQuery, useGetExperimentValidityQuery, useMarkValiditySeenMutation } = validityApi;