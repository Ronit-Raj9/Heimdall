import { useGetCanariesQuery } from "@/lib/store/apis/canaryApi";
import { useMemo } from "react";
import type { DriftMonitorNames } from "./experiments.utils";

/**
 * The names of the drift monitors that exist, for labelling a deleted monitor's runs (N257).
 * Undefined until GET /canaries answers, or when it fails, so nothing reads as deleted by mistake.
 */
export function useDriftMonitorNames(): DriftMonitorNames | undefined {
	const list = useGetCanariesQuery();
	return useMemo(() => (list.data ? new Set((list.data.canaries ?? []).map((c) => c.name)) : undefined), [list.data]);
}