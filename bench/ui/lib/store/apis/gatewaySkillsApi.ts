import type { GwSkill, GwSkillVersion } from "@/lib/types/gateway";
import { benchApi } from "./benchApi";

/** The answer of POST /agents/{id}/skills/from-bifrost (handlers.GatewaySkillImport); also the body of its 422 when the safety scan blocks the skill. */
export interface GwSkillImport {
	folder: string;
	extra_path: string;
	scan: { installed: boolean; record?: { decision?: string } | null };
}

const enc = encodeURIComponent;

/** The skills of the org's Bifrost and the import of one version into an agent (D3 P2). */
export const gatewaySkillsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getGatewaySkills: builder.query<{ skills: GwSkill[] }, void>({
			query: () => "/gateway/skills",
		}),
		getGatewaySkillVersions: builder.query<{ versions: GwSkillVersion[] }, string>({
			query: (id) => `/gateway/skills/${enc(id)}/versions`,
		}),
		importSkillFromBifrost: builder.mutation<GwSkillImport, { agent: string; skill_id: string; version: string }>({
			query: ({ agent, ...body }) => ({ url: `/agents/${enc(agent)}/skills/from-bifrost`, method: "POST", body }),
		}),
	}),
});

export const { useGetGatewaySkillsQuery, useGetGatewaySkillVersionsQuery, useImportSkillFromBifrostMutation } = gatewaySkillsApi;