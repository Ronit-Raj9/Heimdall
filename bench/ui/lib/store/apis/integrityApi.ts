import { withOrgParam } from "./orgParam";
import type { AwarenessResponse, MonitorCertificationListResponse, MonitorCertificationResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";

/**
 * Eval integrity area (S2): the eval-awareness scan, monitor certifications and the compliance
 * pack, injected into the bench API so the shared endpoint list stays untouched.
 */
export const integrityApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getAwareness: builder.query<AwarenessResponse, string>({
			query: (expId) => `/experiments/${encodeURIComponent(expId)}/awareness`,
		}),
		listMonitorCertifications: builder.query<MonitorCertificationListResponse, void>({
			query: () => "/monitors/certifications",
		}),
		getMonitorCertification: builder.query<MonitorCertificationResponse, string>({
			query: (certId) => `/monitors/certifications/${encodeURIComponent(certId)}`,
		}),
	}),
});

export type PackFormat = "md" | "json";

/** The download URL of an experiment's compliance pack (GET /experiments/{id}/compliance). */
export function compliancePackUrl(expId: string, format: PackFormat): string {
	return withOrgParam(`${BENCH_API_BASE}/experiments/${encodeURIComponent(expId)}/compliance?format=${format}`);
}

/** The Markdown report of a monitor certification. */
export function monitorReportUrl(certId: string): string {
	return withOrgParam(`${BENCH_API_BASE}/monitors/certifications/${encodeURIComponent(certId)}/report.md`);
}

export const { useGetAwarenessQuery, useListMonitorCertificationsQuery, useGetMonitorCertificationQuery } = integrityApi;