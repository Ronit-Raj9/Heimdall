import { benchApi } from "./benchApi";

/**
 * Public status page (lane Z14, bench/handlers/statuspage.go, bench/statuspage): each platform
 * component's health and the operator's incident notes. GET /status needs no sign-in. Types mirror
 * the Go JSON.
 */

export type StatusLevel = "operational" | "maintenance" | "degraded" | "partial_outage" | "major_outage" | "unknown";
export type IncidentStatus = "investigating" | "identified" | "monitoring" | "resolved" | "scheduled";
export type IncidentImpact = "none" | "minor" | "major" | "critical" | "maintenance";

export interface StatusComponent {
	id: string;
	name: string;
	level: StatusLevel;
	detail?: string;
	/** The open incident that sets the level, if any. */
	incident?: string;
}

export interface IncidentUpdate {
	at: string;
	status: IncidentStatus;
	message: string;
}

export interface Incident {
	id: string;
	title: string;
	status: IncidentStatus;
	impact: IncidentImpact;
	components?: string[];
	started_at: string;
	scheduled_for?: string;
	resolved_at?: string;
	/** Oldest first. */
	updates: IncidentUpdate[];
}

export interface StatusPage {
	level: StatusLevel;
	components: StatusComponent[];
	incidents: Incident[];
	recent: Incident[];
	version?: string;
	checked_at: string;
}

export const statusApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getStatusPage: builder.query<StatusPage, void>({
			query: () => "/status",
			keepUnusedDataFor: 15,
		}),
	}),
});

export const { useGetStatusPageQuery } = statusApi;
