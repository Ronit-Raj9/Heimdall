import { GatewayEffectView } from "@/components/gateway/gatewayEffectCard";
import { effectOf, useGetBifrostSettingQuery, useGetGatewayEffectOptionalQuery } from "@/lib/store/apis/gatewayApi";

// M4-7: the shared optional query (?optional=1) answers null for a comparison that is no feature test.
export { effectOf };

/** The feature test's Effect on the Result tab; renders nothing when the comparison is not a feature test. */
export function GatewayEffectSlot({ expId }: { expId: string }) {
	const answer = useGetGatewayEffectOptionalQuery(expId);
	const effect = effectOf(answer.data);
	const setting = useGetBifrostSettingQuery(expId, { skip: !effect });
	if (!effect) return null;
	return <GatewayEffectView effect={effect} setting={setting.data} />;
}
