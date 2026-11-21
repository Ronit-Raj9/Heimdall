import type { DocBody, DocList, QuickRunRequest, QuickRunResponse } from "@/lib/types/welcome";
import { benchApi } from "./benchApi";

/** Onboarding (lane L21): in-app docs and the wizard's one-call run (POST /quickrun, lane L28). */
export const welcomeApi = benchApi.enhanceEndpoints({ addTagTypes: ["WelcomeDocs"] }).injectEndpoints({
	endpoints: (builder) => ({
		listDocs: builder.query<DocList, void>({
			query: () => "/docs",
			providesTags: ["WelcomeDocs"],
		}),
		getDoc: builder.query<DocBody, string>({
			query: (name) => `/docs/${encodeURIComponent(name)}`,
			providesTags: (_r, _e, name) => [{ type: "WelcomeDocs", id: name }],
		}),
		welcomeQuickRun: builder.mutation<QuickRunResponse, QuickRunRequest>({
			query: (body) => ({ url: "/quickrun", method: "POST", body }),
			invalidatesTags: [{ type: "BenchExperiment", id: "LIST" }],
		}),
	}),
});

export const { useListDocsQuery, useGetDocQuery, useWelcomeQuickRunMutation } = welcomeApi;
