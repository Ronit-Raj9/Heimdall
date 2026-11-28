import type { GwEffect } from "@/lib/types/gateway";
import { benchApi } from "./benchApi";

/** One prompt of GET /gateway/prompts (handlers.GatewayPrompt). */
export interface GwPrompt {
	id: string;
	name: string;
}

/** One version of GET /gateway/prompts/{id}/versions (handlers.GatewayPromptVersion). */
export interface GwPromptVersion {
	version: number;
	commit_message: string;
	created_at: string;
	messages: number;
	model: string;
}

const enc = encodeURIComponent;

/** Round 5 L1: the prompts of the org's Bifrost for the prompt version test, and the saved feature results across comparisons. */
export const gatewayPromptsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getGatewayPrompts: builder.query<{ prompts: GwPrompt[] }, void>({
			query: () => "/gateway/prompts",
		}),
		getGatewayPromptVersions: builder.query<{ versions: GwPromptVersion[] }, string>({
			query: (id) => `/gateway/prompts/${enc(id)}/versions`,
		}),
		getGatewayEffects: builder.query<{ effects: GwEffect[] }, { limit?: number } | void>({
			query: (arg) => `/gateway/effects?limit=${arg?.limit ?? 20}`,
		}),
	}),
});

export const { useGetGatewayPromptsQuery, useGetGatewayPromptVersionsQuery, useGetGatewayEffectsQuery } = gatewayPromptsApi;