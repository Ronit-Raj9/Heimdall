export type OptionsState = "loading" | "error" | "empty" | "ready";

/** The query fields a picker reads. Works with any RTK Query result. */
export interface OptionsQuery {
	isLoading?: boolean;
	isFetching?: boolean;
	isError?: boolean;
	data?: unknown;
}

/** How many options a query result holds: an array's length, or an object's `items`/`data` array. */
function countOf(data: unknown): number {
	if (Array.isArray(data)) return data.length;
	if (data && typeof data === "object") {
		for (const k of ["items", "data", "models", "rows"]) {
			const v = (data as Record<string, unknown>)[k];
			if (Array.isArray(v)) return v.length;
		}
	}
	return 0;
}

/**
 * What a picker shows (X-107), in the order that keeps a failed query from reading "No models":
 * loading, then error, then empty, then ready. A pure function, so it is a hook in name only;
 * call it in render: `const state = useOptionsState(modelsQuery)`.
 */
export function optionsState(q: OptionsQuery, count = countOf(q.data)): OptionsState {
	if (q.isLoading || (q.isFetching && q.data === undefined)) return "loading";
	if (q.isError) return "error";
	if (count === 0) return "empty";
	return "ready";
}

export function useOptionsState(q: OptionsQuery, count?: number): OptionsState {
	return optionsState(q, count);
}
