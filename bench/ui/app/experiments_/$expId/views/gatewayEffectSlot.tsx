import { GatewayEffectView } from "@/components/gateway/gatewayEffectCard";
import { gatewayApi, useGetBifrostSettingQuery } from "@/lib/store/apis/gatewayApi";
import type { GwEffect } from "@/lib/types/gateway";

/**
 * M4-7: the gateway effect asked with ?optional=1, so a comparison that is no feature test answers
 * 200 {"effect": null} instead of a 404 on every Result. A server without the parameter still sends
 * the effect itself (feature test) or a 404 (read as nothing).
 */
const optionalEffectApi = gatewayApi.injectEndpoints({
	endpoints: (builder) => ({
		getGatewayEffectOptional: builder.query<GwEffect | { effect: null } | null, string>({
			query: (expId) => `/experiments/${encodeURIComponent(expId)}/gateway-effect?optional=1`,
			providesTags: (_r, _e, expId) => [{ type: "Gateway" as const, id: `effect:${expId}` }],
		}),
	}),
});

/** The effect in an answer, or null for {"effect": null}, an empty answer or anything that is no effect. Pure. */
export function effectOf(answer: unknown): GwEffect | null {
	if (!answer || typeof answer !== "object") return null;
	if ("effect" in answer && (answer as { effect: unknown }).effect === null) return null;
	const e = answer as Partial<GwEffect>;
	return typeof e.feature === "string" && typeof e.state === "string" ? (answer as GwEffect) : null;
}

/** The feature test's Effect on the Result tab; renders nothing when the comparison is not a feature test. */
export function GatewayEffectSlot({ expId }: { expId: string }) {
	const answer = optionalEffectApi.useGetGatewayEffectOptionalQuery(expId);
	const effect = effectOf(answer.data);
	const setting = useGetBifrostSettingQuery(expId, { skip: !effect });
	if (!effect) return null;
	return <GatewayEffectView effect={effect} setting={setting.data} />;
}