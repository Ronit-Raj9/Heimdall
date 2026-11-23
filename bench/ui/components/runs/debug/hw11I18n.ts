import i18n from "@/lib/i18n";
import type hw11En from "@/locales/en/hw11.json";

// Types the "hw11" namespace (locales/en/hw11.json): fix wave 3 lane H11 texts (fork banner and
// chip, follow-up link, step 3 setup checks).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw11: typeof hw11En;
	}
}

/** An "hw11" string, also outside a component. */
export function hw11(key: string, values?: Record<string, string | number>): string {
	return (i18n.t as unknown as (k: string, o: Record<string, unknown>) => string)(key, { ns: "hw11", ...values });
}