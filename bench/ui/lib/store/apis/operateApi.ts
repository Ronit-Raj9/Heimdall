import type { OperateStatus } from "@/lib/types/operate";
import { benchApi } from "./benchApi";

/** The Operate area (web parity lane L20): what this server can run. Jobs go through POST /jobs. */
export const operateApi = benchApi.enhanceEndpoints({ addTagTypes: ["OperateStatus"] }).injectEndpoints({
	endpoints: (builder) => ({
		getOperateStatus: builder.query<OperateStatus, void>({
			query: () => ({ url: "/operate/status" }),
			providesTags: ["OperateStatus"],
		}),
	}),
});

export const { useGetOperateStatusQuery } = operateApi;
