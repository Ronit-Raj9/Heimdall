import { useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { newestExperiment } from "./experiments.utils";

export interface ExperimentIdState {
	/** The experiment to show: the URL's, else the newest one. Null while loading or when none exist. */
	expId: string | null;
	isLoading: boolean;
	error: unknown;
	/** The server answered with no experiments. */
	isEmpty: boolean;
	refetch: () => void;
}

/** The experiment named in the URL, or the newest one on the server when the URL names none. */
export function useExperimentId(fromUrl: string | null): ExperimentIdState {
	const list = useListExperimentsQuery(undefined, { skip: Boolean(fromUrl) });
	if (fromUrl) return { expId: fromUrl, isLoading: false, error: undefined, isEmpty: false, refetch: () => undefined };
	const newest = list.data ? newestExperiment(list.data.experiments) : null;
	return {
		expId: newest?.exp_id ?? null,
		isLoading: list.isLoading,
		error: list.error,
		isEmpty: Boolean(list.data) && newest === null,
		refetch: () => void list.refetch(),
	};
}