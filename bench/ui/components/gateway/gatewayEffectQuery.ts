import { gatewayApi } from "@/lib/store/apis/gatewayApi";
import type { GwEffect } from "@/lib/types/gateway";

/**
 * The gateway effect path with `?optional=1`: the server then answers {"effect": null} for a
 * comparison that is not a feature test instead of a 404 (M4-7). An older server ignores the
 * parameter and still answers 404, which the card also reads as "no effect".
 */
export function gatewayEffectPath(expId: string): string {
	return `/experiments/${encodeURIComponent(expId)}/gateway-effect?optional=1`;
}

// Injected here because lib/store/apis/gatewayApi.ts belongs to another lane; it shares the Gateway tag.
const optionalEffectApi = gatewayApi.injectEndpoints({
	endpoints: (builder) => ({
		getGatewayEffectOptional: builder.query<GwEffect | { effect: GwEffect | null }, string>({
			query: gatewayEffectPath,
			providesTags: (_r, _e, expId) => [{ type: "Gateway", id: `effect:${expId}` }],
		}),
	}),
});

export const { useGetGatewayEffectOptionalQuery } = optionalEffectApi;
