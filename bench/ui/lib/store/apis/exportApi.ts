import { withOrgParam } from "./orgParam";
import type { ExportRunRow } from "@/lib/types/bench";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";

/**
 * W10 export (docs/export.md): the run table of an experiment, GET /experiments/{id}/export?format=jsonl,
 * read as JSON lines, injected into the bench API so the shared endpoint list stays untouched. The
 * other formats are file downloads the browser fetches from experimentExportUrl and suiteHarborUrl.
 */
export const exportApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getExperimentRunTable: builder.query<ExportRunRow[], string>({
			query: (expId) => ({ url: experimentExportPath(expId, "jsonl"), responseHandler: "text" }),
			transformResponse: (body: string) => parseJSONLines<ExportRunRow>(body),
			// Refetched with the experiment's other results when a run settles (QC2-28-05).
			providesTags: (_result, _error, expId) => [{ type: "BenchExperimentResult", id: expId }],
		}),
	}),
});

/** The request path of an experiment export, the id escaped as one path segment. */
export function experimentExportPath(expId: string, format: "eee" | "jsonl" | "csv", allowSynthetic = false): string {
	const q = new URLSearchParams({ format });
	if (allowSynthetic) q.set("allow_synthetic", "true");
	return `/experiments/${encodeURIComponent(expId)}/export?${q.toString()}`;
}

/** The download URL of an experiment export. */
export function experimentExportUrl(expId: string, format: "eee" | "jsonl" | "csv", allowSynthetic = false): string {
	return withOrgParam(BENCH_API_BASE + experimentExportPath(expId, format, allowSynthetic));
}

/** The download URL of a suite's Harbor tar (verifiers withheld). */
export function suiteHarborUrl(suiteId: string): string {
	return withOrgParam(`${BENCH_API_BASE}/suites/${encodeURIComponent(suiteId)}/export?format=harbor`);
}

/** Parses JSON lines; a blank line is skipped, a line that does not parse throws. */
export function parseJSONLines<T>(body: string): T[] {
	return body
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line.length > 0)
		.map((line) => JSON.parse(line) as T);
}

export const { useGetExperimentRunTableQuery } = exportApi;