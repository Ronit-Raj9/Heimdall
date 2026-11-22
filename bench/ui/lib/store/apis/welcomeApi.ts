import type { DocBody, DocList, QuickRunRequest, QuickRunResponse } from "@/lib/types/welcome";
import { benchApi } from "./benchApi";

/** GET /docs, with the snippet a search hit carries (fix wave 2, N138). */
export interface DocSearchList extends DocList {
	docs: (DocList["docs"][number] & { snippet?: string })[];
}

/** Onboarding (lane L21): in-app docs and the wizard's one-call run (POST /quickrun, lane L28). */
export const welcomeApi = benchApi.enhanceEndpoints({ addTagTypes: ["WelcomeDocs"] }).injectEndpoints({
	endpoints: (builder) => ({
		// q (2 to 80 characters) searches titles, names and bodies; each hit then carries a snippet.
		listDocs: builder.query<DocSearchList, string | void>({
			query: (q) => (q ? `/docs?q=${encodeURIComponent(q)}` : "/docs"),
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