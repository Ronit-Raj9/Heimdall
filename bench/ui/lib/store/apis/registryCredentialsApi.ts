import type { RegistryCredential, RegistryCredentialCreate, RegistryCredentialList } from "@/lib/types/registryCredentials";
import { benchApi } from "./benchApi";

/** Private image registry logins (handlers/registrycreds.go): admins add and remove them. */
export const registryCredentialsApi = benchApi.enhanceEndpoints({ addTagTypes: ["RegistryCredentials"] }).injectEndpoints({
	endpoints: (builder) => ({
		listRegistryCredentials: builder.query<RegistryCredentialList, void>({
			query: () => "/registry-credentials",
			providesTags: [{ type: "RegistryCredentials", id: "LIST" }],
		}),
		createRegistryCredential: builder.mutation<RegistryCredential, RegistryCredentialCreate>({
			query: (body) => ({ url: "/registry-credentials", method: "POST", body }),
			invalidatesTags: [{ type: "RegistryCredentials", id: "LIST" }],
		}),
		deleteRegistryCredential: builder.mutation<{ id: string; deleted: boolean }, string>({
			query: (id) => ({ url: `/registry-credentials/${encodeURIComponent(id)}`, method: "DELETE" }),
			invalidatesTags: [{ type: "RegistryCredentials", id: "LIST" }],
		}),
	}),
});

export const { useListRegistryCredentialsQuery, useCreateRegistryCredentialMutation, useDeleteRegistryCredentialMutation } = registryCredentialsApi;
