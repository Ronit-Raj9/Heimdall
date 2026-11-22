import i18n from "@/lib/i18n";
import type fw05En from "@/locales/en/fw05.json";

// Types the "fw05" namespace (locales/en/fw05.json): wizard step 5 summary, budget and name texts.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		fw05: typeof fw05En;
	}
}

/** A "fw05" string outside a component (summaryLines and budgetLine are plain functions). */
export function fw05(key: string, values?: Record<string, string>): string {
	return (i18n.t as unknown as (k: string, o: Record<string, unknown>) => string)(key, { ns: "fw05", ...values });
}