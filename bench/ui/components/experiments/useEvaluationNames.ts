import { useListEvaluationsLightQuery } from "@/lib/store/apis/evaluationsApi";
import { useMemo } from "react";
import type { EvaluationNames } from "./experiments.utils";

/**
 * Evaluation id to evaluation name, for naming the benchmarks of an evaluation in experiment
 * lists and pickers. Every caller shares one cached light GET /evaluations?fields=light (M4-77), of
 * every status (an empty status list sends none, so running evaluations are named too); while it loads or when it
 * fails the map is empty and lists fall back to their older names.
 */
const ALL_STATUSES = { status: [] as string[] };

export function useEvaluationNames(): EvaluationNames {
	const list = useListEvaluationsLightQuery(ALL_STATUSES);
	return useMemo(() => new Map((list.data ?? []).map((ev) => [ev.id, ev.spec?.name?.trim() || ev.id] as const)), [list.data]);
}