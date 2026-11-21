import type { AuditVerdictResponse } from "@/lib/types/analysis";
import { benchApi } from "./benchApi";

/** Largest audit table the page sends; the server's body limit is 1 MiB. */
export const AUDIT_MAX_BYTES = 1 << 20;

/**
 * Web parity lane L15: POST /audit with a per-task CSV table (task,a_pass,a_n,b_pass,b_n), the web
 * form of `bifrost-bench audit table.csv`. The analysis jobs themselves go through POST /jobs.
 */
export const analysisApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		auditTable: builder.mutation<AuditVerdictResponse, { csv: string }>({
			query: ({ csv }) => auditRequest(csv),
		}),
	}),
});

/** The request of POST /audit for a CSV table. */
export function auditRequest(csv: string) {
	return { url: "/audit", method: "POST", body: csv, headers: { "Content-Type": "text/csv" } };
}

export const { useAuditTableMutation } = analysisApi;
