// Identity (Wave A): email verification and the server admin's signup mode, injected into benchApi
// so they share its base query (CSRF header, 401 handling).
import type { SignupSettings, SignupSettingsRequest, VerifyEmailRequest, VerifyEmailResult, VerifySent } from "@/lib/types/identity";
import { benchApi } from "./benchApi";

export const identityApi = benchApi.enhanceEndpoints({ addTagTypes: ["SignupSettings", "AuthMe"] }).injectEndpoints({
	endpoints: (builder) => ({
		verifyEmail: builder.mutation<VerifyEmailResult, VerifyEmailRequest>({
			query: (body) => ({ url: "/auth/verify-email", method: "POST", body }),
			// The banner reads email_verified from /auth/me.
			invalidatesTags: ["AuthMe"],
		}),
		resendVerification: builder.mutation<VerifySent, void>({
			query: () => ({ url: "/auth/verify-email/resend", method: "POST" }),
		}),
		getSignupSettings: builder.query<SignupSettings, void>({
			query: () => "/admin/signup",
			providesTags: ["SignupSettings"],
		}),
		setSignupSettings: builder.mutation<SignupSettings, SignupSettingsRequest>({
			query: (body) => ({ url: "/admin/signup", method: "POST", body }),
			invalidatesTags: ["SignupSettings"],
		}),
	}),
});

export const { useVerifyEmailMutation, useResendVerificationMutation, useGetSignupSettingsQuery, useSetSignupSettingsMutation } = identityApi;
