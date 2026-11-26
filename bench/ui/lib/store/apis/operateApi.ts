import { withOrgParam } from "./orgParam";
import type { OperateBackupList, OperateStatus } from "@/lib/types/operate";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";

/** The Operate area (web parity lane L20): what this server can run. Jobs go through POST /jobs. */
export const operateApi = benchApi.enhanceEndpoints({ addTagTypes: ["OperateStatus", "OperateBackups"] }).injectEndpoints({
	endpoints: (builder) => ({
		getOperateStatus: builder.query<OperateStatus, void>({
			query: () => ({ url: "/operate/status" }),
			providesTags: ["OperateStatus"],
		}),
		/** Evidence backup zips of the organization (evidence.backup jobs), newest first. */
		getOperateBackups: builder.query<OperateBackupList, void>({
			query: () => ({ url: "/operate/backups" }),
			providesTags: ["OperateBackups"],
		}),
		deleteOperateBackup: builder.mutation<void, string>({
			query: (name) => ({ url: `/operate/backups/${encodeURIComponent(name)}`, method: "DELETE" }),
			invalidatesTags: ["OperateBackups"],
		}),
	}),
});

export const { useGetOperateStatusQuery, useGetOperateBackupsQuery, useDeleteOperateBackupMutation } = operateApi;

/** The download URL of an evidence backup zip. */
export function operateBackupUrl(name: string): string {
	return withOrgParam(`${BENCH_API_BASE}/operate/backups/${encodeURIComponent(name)}`);
}
