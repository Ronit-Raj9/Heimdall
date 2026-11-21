import type {
	ImageRequest,
	ImageRequestBody,
	ImageRequestList,
	ImageRequestStatus,
	KillSwitch,
	OrgLimits,
	OrgQuota,
	PoolStatus,
} from "@/lib/types/sharedpool";
import { benchApi } from "./benchApi";

/**
 * Shared runner pool and guardrails (web parity lane L03): the org's pool status and image
 * requests, and the server admin routes (quotas, image approval, kill switch) the admin view uses.
 */
export const sharedpoolApi = benchApi
	.enhanceEndpoints({ addTagTypes: ["SharedPool", "ImageRequests", "OrgQuota"] })
	.injectEndpoints({
		endpoints: (builder) => ({
			getPoolStatus: builder.query<PoolStatus, void>({
				query: () => "/pool",
				providesTags: ["SharedPool"],
			}),
			listImageRequests: builder.query<ImageRequestList, void>({
				query: () => "/images",
				providesTags: ["ImageRequests"],
			}),
			requestImage: builder.mutation<ImageRequest, ImageRequestBody>({
				query: (body) => ({ url: "/images", method: "POST", body }),
				invalidatesTags: ["ImageRequests"],
			}),
			adminListImages: builder.query<ImageRequestList, ImageRequestStatus | void>({
				query: (status) => (status ? `/admin/images?status=${status}` : "/admin/images"),
				providesTags: ["ImageRequests"],
			}),
			adminDecideImage: builder.mutation<ImageRequest, { id: string; approve: boolean; note?: string }>({
				query: ({ id, approve, note }) => ({
					url: `/admin/images/${encodeURIComponent(id)}/${approve ? "approve" : "reject"}`,
					method: "POST",
					body: note ? { note } : {},
				}),
				invalidatesTags: ["ImageRequests"],
			}),
			adminGetQuota: builder.query<OrgQuota, string>({
				query: (orgId) => `/admin/quotas/${encodeURIComponent(orgId)}`,
				providesTags: (_r, _e, orgId) => [{ type: "OrgQuota", id: orgId }],
			}),
			adminSetQuota: builder.mutation<OrgQuota, { orgId: string; limits: OrgLimits }>({
				query: ({ orgId, limits }) => ({ url: `/admin/quotas/${encodeURIComponent(orgId)}`, method: "PUT", body: limits }),
				invalidatesTags: (_r, _e, { orgId }) => [{ type: "OrgQuota", id: orgId }, "SharedPool"],
			}),
			adminGetKillSwitch: builder.query<KillSwitch, void>({
				query: () => "/admin/killswitch",
				providesTags: ["SharedPool"],
			}),
			adminSetKillSwitch: builder.mutation<KillSwitch, KillSwitch>({
				query: (body) => ({ url: "/admin/killswitch", method: "POST", body }),
				invalidatesTags: ["SharedPool"],
			}),
		}),
	});

export const {
	useGetPoolStatusQuery,
	useListImageRequestsQuery,
	useRequestImageMutation,
	useAdminListImagesQuery,
	useAdminDecideImageMutation,
	useAdminGetQuotaQuery,
	useAdminSetQuotaMutation,
	useAdminGetKillSwitchQuery,
	useAdminSetKillSwitchMutation,
} = sharedpoolApi;
