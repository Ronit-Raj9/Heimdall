import i18n from "@/lib/i18n";
import type ufl3En from "@/locales/en/ufl3.json";

// Types the "ufl3" namespace (locales/en/ufl3.json): the evaluate wizard, round 5 lane L3: model switch mode and the held-out split.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufl3: typeof ufl3En;
	}
}

export {};
/** A "ufl3" string outside a component (specProblem is a plain function). */
export function ufl3(key: string, values?: Record<string, string | number>): string {
	return (i18n.t as unknown as (k: string, o: Record<string, unknown>) => string)(key, { ns: "ufl3", ...values });
}
