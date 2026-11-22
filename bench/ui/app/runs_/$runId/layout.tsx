import { createFileRoute } from "@tanstack/react-router";
import RunPage from "./page";

/** ?tab= opens a tab, ?step= selects a step on Step by step (a trace search hit links there). */
interface RunSearch {
	tab?: string;
	step?: number;
	[key: string]: unknown;
}

/** Keeps every search param; tab only when a string, step only when a positive integer. */
function validateRunSearch(search: Record<string, unknown>): RunSearch {
	const { tab, step, ...rest } = search;
	const out: RunSearch = { ...rest };
	if (typeof tab === "string" && tab) out.tab = tab;
	const n = typeof step === "number" ? step : typeof step === "string" && /^\d+$/.test(step) ? Number(step) : NaN;
	if (Number.isInteger(n) && n > 0) out.step = n;
	return out;
}

// "runs_" keeps this route out of /runs (the list renders no Outlet); the path is /runs/$runId.
export const Route = createFileRoute("/runs_/$runId")({
	validateSearch: validateRunSearch,
	component: RunPage,
});