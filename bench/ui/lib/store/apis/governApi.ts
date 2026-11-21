import type { PolicyDocument, PolicyList, PolicySaveRequest, PolicyValidation } from "@/lib/types/govern";
import { benchApi } from "./benchApi";

/** Govern area (web parity lane L16): the release policy editor over handlers/policies.go. */
export const governApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchPolicy"] }).injectEndpoints({
	endpoints: (builder) => ({
		listPolicies: builder.query<PolicyList, void>({
			query: () => "/policies",
			providesTags: [{ type: "BenchPolicy", id: "LIST" }],
		}),
		getPolicy: builder.query<PolicyDocument, string>({
			query: (file) => policyPath(file),
			providesTags: (_r, _e, file) => [{ type: "BenchPolicy", id: file }],
		}),
		savePolicy: builder.mutation<PolicyDocument, { file: string; body: PolicySaveRequest }>({
			query: ({ file, body }) => ({ url: policyPath(file), method: "PUT", body }),
			invalidatesTags: (_r, _e, { file }) => [
				{ type: "BenchPolicy", id: "LIST" },
				{ type: "BenchPolicy", id: file },
			],
		}),
		validatePolicy: builder.mutation<PolicyValidation, PolicySaveRequest>({
			query: (body) => ({ url: "/policies/validate", method: "POST", body }),
		}),
		deletePolicy: builder.mutation<void, string>({
			query: (file) => ({ url: policyPath(file), method: "DELETE" }),
			invalidatesTags: (_r, _e, file) => [
				{ type: "BenchPolicy", id: "LIST" },
				{ type: "BenchPolicy", id: file },
			],
		}),
	}),
});

/** A policy file's path, escaped as one segment. */
export function policyPath(file: string): string {
	return `/policies/${encodeURIComponent(file)}`;
}

/** A file name the server accepts: lower case, digits, '.', '_' or '-', ending in .yaml or .yml. */
export function normalizePolicyFile(input: string): string {
	let f = input.trim().toLowerCase().replace(/\s+/g, "-");
	if (f !== "" && !/\.ya?ml$/.test(f)) f += ".yaml";
	return f;
}

export function isValidPolicyFile(file: string): boolean {
	return /^[a-z0-9][a-z0-9._-]{0,62}\.ya?ml$/.test(file);
}

export const {
	useListPoliciesQuery,
	useGetPolicyQuery,
	useSavePolicyMutation,
	useValidatePolicyMutation,
	useDeletePolicyMutation,
} = governApi;
