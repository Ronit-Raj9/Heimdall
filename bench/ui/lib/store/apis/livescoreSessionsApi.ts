import type { LiveSessionListResponse } from "@/components/livescore/livescore.types";
import { benchApi } from "./benchApi";

/** Filters of GET /livescore/sessions (handlers/livescore.go getSessions). */
export interface LiveSessionsArgs {
	version?: string;
	flag?: string;
	limit?: number;
}

/** GET /livescore/sessions?version=&flag=&limit=: empty filters are left out. */
export function liveSessionsUrl(a: LiveSessionsArgs): string {
	const q = new URLSearchParams();
	if (a.version) q.set("version", a.version);
	if (a.flag) q.set("flag", a.flag);
	if (a.limit) q.set("limit", String(a.limit));
	const s = q.toString();
	return s ? `/livescore/sessions?${s}` : "/livescore/sessions";
}

export const livescoreSessionsApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchLive"] }).injectEndpoints({
	endpoints: (builder) => ({
		getLiveSessions: builder.query<LiveSessionListResponse, LiveSessionsArgs>({
			query: (a) => liveSessionsUrl(a),
			providesTags: [{ type: "BenchLive", id: "STATUS" }],
		}),
	}),
});

export const { useGetLiveSessionsQuery } = livescoreSessionsApi;
