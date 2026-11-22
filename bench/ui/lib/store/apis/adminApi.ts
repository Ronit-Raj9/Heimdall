import type {
	AdminContainer,
	AdminImage,
	AdminLimits,
	AdminOrg,
	AdminOverview,
	AdminQuota,
	AdminUsage,
	AdminUser,
} from "@/lib/types/admin";
import type { FetchArgs } from "@reduxjs/toolkit/query/react";
import { benchApi } from "./benchApi";

// Server admin page (web parity L23). Platform routes; the server answers 403 unless the caller
// is listed in HEIMDALL_SERVER_ADMINS, so the page only queries when the session says so.

const seg = (value: string) => encodeURIComponent(value);

/** POST /admin/orgs/{id}/quota. */
export function setQuotaRequest(orgId: string, limits: AdminLimits): FetchArgs {
	return { url: `/admin/orgs/${seg(orgId)}/quota`, method: "POST", body: limits };
}

/** POST /admin/users/{id}/disable. */
export function disableUserRequest(userId: string, disabled: boolean): FetchArgs {
	return { url: `/admin/users/${seg(userId)}/disable`, method: "POST", body: { disabled } };
}

/** POST /admin/containers/{org}/{id}/remove: a server admin removes an offline worker's record (N11). */
export function removeAdminRunnerRequest(orgId: string, runnerId: string): FetchArgs {
	return { url: `/admin/containers/${seg(orgId)}/${seg(runnerId)}/remove`, method: "POST" };
}

/** POST /admin/images/{id}/approve or /reject (shared pool area, L03). */
export function imageDecisionRequest(id: string, decision: "approve" | "reject", reason?: string): FetchArgs {
	return { url: `/admin/images/${seg(id)}/${decision}`, method: "POST", body: reason ? { note: reason } : {} };
}

export const adminApi = benchApi.enhanceEndpoints({ addTagTypes: ["AdminUsers", "AdminOrgs", "AdminKill", "AdminImages", "AdminContainers"] }).injectEndpoints({
	endpoints: (builder) => ({
		getAdminOverview: builder.query<AdminOverview, void>({
			query: () => "/admin/overview",
			providesTags: ["AdminUsers", "AdminOrgs", "AdminKill", "AdminContainers"],
		}),
		listAdminUsers: builder.query<{ users: AdminUser[] }, void>({
			query: () => "/admin/users",
			providesTags: ["AdminUsers"],
		}),
		setUserDisabled: builder.mutation<{ id: string; disabled: boolean }, { id: string; disabled: boolean }>({
			query: ({ id, disabled }) => disableUserRequest(id, disabled),
			invalidatesTags: ["AdminUsers"],
		}),
		listAdminOrgs: builder.query<{ orgs: AdminOrg[] }, void>({
			query: () => "/admin/orgs",
			providesTags: ["AdminOrgs"],
		}),
		getOrgQuota: builder.query<AdminQuota, string>({
			query: (orgId) => `/admin/orgs/${seg(orgId)}/quota`,
			providesTags: ["AdminOrgs"],
		}),
		setOrgQuota: builder.mutation<AdminQuota, { orgId: string; limits: AdminLimits }>({
			query: ({ orgId, limits }) => setQuotaRequest(orgId, limits),
			invalidatesTags: ["AdminOrgs"],
		}),
		getKillSwitch: builder.query<{ on: boolean }, void>({
			query: () => "/admin/killswitch",
			providesTags: ["AdminKill"],
		}),
		setKillSwitch: builder.mutation<{ on: boolean }, boolean>({
			query: (on) => ({ url: "/admin/killswitch", method: "POST", body: { on } }),
			invalidatesTags: ["AdminKill"],
		}),
		listAdminContainers: builder.query<{ runners: AdminContainer[]; running_slots: number }, void>({
			query: () => "/admin/containers",
			providesTags: ["AdminContainers"],
		}),
		removeAdminRunner: builder.mutation<unknown, { orgId: string; runnerId: string }>({
			query: ({ orgId, runnerId }) => removeAdminRunnerRequest(orgId, runnerId),
			invalidatesTags: ["AdminContainers"],
		}),
		listAdminImages: builder.query<{ images: AdminImage[] }, AdminImage["status"] | void>({
			query: (status) => (status ? `/admin/images?status=${encodeURIComponent(status)}` : "/admin/images"),
			providesTags: ["AdminImages"],
		}),
		decideImage: builder.mutation<unknown, { id: string; decision: "approve" | "reject"; reason?: string }>({
			query: ({ id, decision, reason }) => imageDecisionRequest(id, decision, reason),
			invalidatesTags: ["AdminImages", "AdminOrgs"],
		}),
		getAdminUsage: builder.query<AdminUsage, { from?: string; to?: string }>({
			query: (params) => ({ url: "/admin/usage", params: Object.fromEntries(Object.entries(params).filter(([, v]) => v)) }),
		}),
	}),
});

export const {
	useGetAdminOverviewQuery,
	useListAdminUsersQuery,
	useSetUserDisabledMutation,
	useListAdminOrgsQuery,
	useGetOrgQuotaQuery,
	useSetOrgQuotaMutation,
	useGetKillSwitchQuery,
	useSetKillSwitchMutation,
	useListAdminContainersQuery,
	useRemoveAdminRunnerMutation,
	useListAdminImagesQuery,
	useDecideImageMutation,
	useGetAdminUsageQuery,
} = adminApi;
