// Account safety (L22): owner reset links and the public reset page, injected into benchApi so they
// share its base query (CSRF header, 401 handling).
import type { ResetInfo, ResetLink, ResetRequest } from "@/lib/types/accountSafety";
import { benchApi } from "./benchApi";

const id = (value: string) => encodeURIComponent(value);

export const accountSafetyApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		createResetLink: builder.mutation<ResetLink, { orgId: string; userId: string }>({
			query: ({ orgId, userId }) => ({ url: `/orgs/${id(orgId)}/members/${id(userId)}/reset-link`, method: "POST" }),
		}),
		getReset: builder.query<ResetInfo, string>({
			query: (token) => `/auth/reset/${id(token)}`,
			keepUnusedDataFor: 0,
		}),
		resetPassword: builder.mutation<void, { token: string } & ResetRequest>({
			query: ({ token, password }) => ({ url: `/auth/reset/${id(token)}`, method: "POST", body: { password } }),
		}),
	}),
});

export const { useCreateResetLinkMutation, useGetResetQuery, useResetPasswordMutation } = accountSafetyApi;
