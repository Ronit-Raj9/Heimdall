import type { FetchArgs } from "@reduxjs/toolkit/query/react";
import { liveMonitorsApi, monitorPath, type MonitorDetailResponse, type MonitorRule } from "./liveMonitorsApi";

/** PUT /live-monitors/{id} (handlers/livemonitors.go MonitorInput): a missing field keeps its value; rule replaces the whole rule. */
export interface MonitorUpdate {
	name?: string;
	tasks?: number;
	interval_min?: number;
	budget_usd_per_day?: number;
	rule?: MonitorRule;
}

export function monitorUpdateRequest(id: string, body: MonitorUpdate): FetchArgs {
	return { url: monitorPath(id), method: "PUT", body };
}

/** Round 5 lane L8: edit a live monitor. */
export const liveMonitorEditApi = liveMonitorsApi.injectEndpoints({
	endpoints: (builder) => ({
		updateLiveMonitor: builder.mutation<MonitorDetailResponse, { id: string; body: MonitorUpdate }>({
			query: ({ id, body }) => monitorUpdateRequest(id, body),
			invalidatesTags: (_r, _e, { id }) => [
				{ type: "BenchLiveMonitor", id: "LIST" },
				{ type: "BenchLiveMonitor", id },
			],
		}),
	}),
});

export const { useUpdateLiveMonitorMutation } = liveMonitorEditApi;