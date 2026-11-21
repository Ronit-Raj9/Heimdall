import type { OrgSecret, OrgSecretCreate, OrgSecretDeleted, OrgSecretList } from "@/lib/types/secrets";
import { benchApi } from "./benchApi";

/**
 * Org secrets (handlers/secrets.go): names only come back, never values. Create, rotate and
 * delete are admin writes. Tools read a secret by name through an external API's auth_env.
 */
export const secretsApi = benchApi.enhanceEndpoints({ addTagTypes: ["OrgSecrets"] }).injectEndpoints({
	endpoints: (builder) => ({
		listSecrets: builder.query<OrgSecretList, void>({
			query: () => "/secrets",
			providesTags: [{ type: "OrgSecrets", id: "LIST" }],
		}),
		createSecret: builder.mutation<OrgSecret, OrgSecretCreate>({
			query: (body) => ({ url: "/secrets", method: "POST", body }),
			invalidatesTags: [{ type: "OrgSecrets", id: "LIST" }],
		}),
		rotateSecret: builder.mutation<OrgSecret, { id: string; value: string }>({
			query: ({ id, value }) => ({ url: `/secrets/${encodeURIComponent(id)}`, method: "PUT", body: { value } }),
			invalidatesTags: [{ type: "OrgSecrets", id: "LIST" }],
		}),
		deleteSecret: builder.mutation<OrgSecretDeleted, string>({
			query: (id) => ({ url: `/secrets/${encodeURIComponent(id)}`, method: "DELETE" }),
			invalidatesTags: [{ type: "OrgSecrets", id: "LIST" }],
		}),
	}),
});

export const { useListSecretsQuery, useCreateSecretMutation, useRotateSecretMutation, useDeleteSecretMutation } = secretsApi;
