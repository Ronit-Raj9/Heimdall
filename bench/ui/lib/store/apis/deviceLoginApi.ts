// Device login (RFC 8628, lane Z02): the approval page's calls, injected into benchApi so they
// share its base query (CSRF header, 401 handling).
import type { DeviceApproved, DeviceInfo } from "@/lib/types/deviceLogin";
import { benchApi } from "./benchApi";

export const deviceLoginApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getDevice: builder.query<DeviceInfo, string>({
			query: (userCode) => `/auth/device?user_code=${encodeURIComponent(userCode)}`,
			keepUnusedDataFor: 0,
		}),
		approveDevice: builder.mutation<DeviceApproved, string>({
			query: (userCode) => ({ url: "/auth/device/approve", method: "POST", body: { user_code: userCode } }),
		}),
		denyDevice: builder.mutation<void, string>({
			query: (userCode) => ({ url: "/auth/device/deny", method: "POST", body: { user_code: userCode } }),
		}),
	}),
});

export const { useGetDeviceQuery, useApproveDeviceMutation, useDenyDeviceMutation } = deviceLoginApi;
