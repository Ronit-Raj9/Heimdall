import { useListEvaluationsQuery } from "@/lib/store/apis/evaluationsApi";
import { useMemo } from "react";
import type { EvaluationNames } from "./experiments.utils";

/**
 * Evaluation id to evaluation name, for naming the benchmarks of an evaluation in experiment
 * lists and pickers. Every caller shares one cached GET /evaluations; while it loads or when it
 * fails the map is empty and lists fall back to their older names.
 */
export function useEvaluationNames(): EvaluationNames {
	const list = useListEvaluationsQuery();
	return useMemo(() => new Map((list.data ?? []).map((ev) => [ev.id, ev.spec?.name?.trim() || ev.id] as const)), [list.data]);
}