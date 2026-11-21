import type { AdminUsageReport, UsageParams, UsageReport } from "@/lib/types/usage";
import { benchApi } from "./benchApi";

/** Query string of a usage request: only the params that have a value. */
export function usageParams(p: UsageParams): Record<string, string> {
	return Object.fromEntries(Object.entries(p).filter((e): e is [string, string] => typeof e[1] === "string" && e[1] !== ""));
}

// Usage and spend (web parity L25): org usage and platform usage (server admins).
export const usageApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchUsage"] }).injectEndpoints({
	endpoints: (builder) => ({
		getUsage: builder.query<UsageReport, UsageParams>({
			query: (p) => ({ url: "/usage", params: usageParams(p) }),
			providesTags: ["BenchUsage"],
		}),
		getAdminUsage: builder.query<AdminUsageReport, Omit<UsageParams, "group_by">>({
			query: (p) => ({ url: "/admin/usage", params: usageParams(p) }),
			providesTags: ["BenchUsage"],
		}),
	}),
});

export const { useGetUsageQuery, useGetAdminUsageQuery } = usageApi;
