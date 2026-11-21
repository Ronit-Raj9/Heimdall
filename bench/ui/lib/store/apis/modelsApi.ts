import type { ModelKey, ModelKeyRequest, ModelKeyTestResponse, ModelsCatalogResponse } from "@/lib/types/models";
import { benchApi } from "./benchApi";

/** Models and keys (handlers/models.go): the provider catalog, provider keys CRUD and the key test. */
export const modelsApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchModels"] }).injectEndpoints({
	endpoints: (builder) => ({
		getModels: builder.query<ModelsCatalogResponse, void>({
			query: () => "/models",
			providesTags: [{ type: "BenchModels", id: "LIST" }],
		}),
		createModelKey: builder.mutation<{ key: ModelKey }, ModelKeyRequest>({
			query: (body) => ({ url: "/models/keys", method: "POST", body }),
			invalidatesTags: [{ type: "BenchModels", id: "LIST" }],
		}),
		updateModelKey: builder.mutation<{ key: ModelKey }, { id: string; body: ModelKeyRequest }>({
			query: ({ id, body }) => ({ url: `/models/keys/${encodeURIComponent(id)}`, method: "PUT", body }),
			invalidatesTags: [{ type: "BenchModels", id: "LIST" }],
		}),
		deleteModelKey: builder.mutation<void, string>({
			query: (id) => ({ url: `/models/keys/${encodeURIComponent(id)}`, method: "DELETE" }),
			invalidatesTags: [{ type: "BenchModels", id: "LIST" }],
		}),
		testModelKey: builder.mutation<ModelKeyTestResponse, string>({
			query: (id) => ({ url: `/models/keys/${encodeURIComponent(id)}/test`, method: "POST" }),
			invalidatesTags: [{ type: "BenchModels", id: "LIST" }],
		}),
	}),
});

export const { useGetModelsQuery, useCreateModelKeyMutation, useUpdateModelKeyMutation, useDeleteModelKeyMutation, useTestModelKeyMutation } =
	modelsApi;
