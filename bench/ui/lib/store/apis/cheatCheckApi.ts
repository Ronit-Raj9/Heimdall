import { benchApi } from "./benchApi";

/** POST /evaluations/{id}/cheat-check (W4-48): runs the evaluation's arms once on the impossible twins of its benchmarks. */
export const cheatCheckApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		startCheatCheck: builder.mutation<{ id: string }, string>({
			query: (id) => ({ url: `/evaluations/${encodeURIComponent(id)}/cheat-check`, method: "POST", body: { yes: true } }),
		}),
	}),
});

export const { useStartCheatCheckMutation } = cheatCheckApi;