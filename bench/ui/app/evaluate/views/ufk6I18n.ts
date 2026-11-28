import i18n from "@/lib/i18n";
import type ufk6En from "@/locales/en/ufk6.json";

// Types the "ufk6" namespace (locales/en/ufk6.json): the evaluate wizard, round 6 lane K6: two models mode.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufk6: typeof ufk6En;
	}
}

/** A "ufk6" string outside a component (specProblem is a plain function). */
export function ufk6(key: string, values?: Record<string, string | number>): string {
	return (i18n.t as unknown as (k: string, o: Record<string, unknown>) => string)(key, { ns: "ufk6", ...values });
}