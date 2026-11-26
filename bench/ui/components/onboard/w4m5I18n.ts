import i18n from "@/lib/i18n";
import type w4m5En from "@/locales/en/w4m5.json";

// Types the "w4m5" namespace (locales/en/w4m5.json): Add benchmark, catalog and onboarding minors.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4m5: typeof w4m5En;
	}
}

/** A string of the w4m5 namespace. */
export function w4m5Text(key: string, opts?: Record<string, unknown>): string {
	return i18n.t(key as never, { ns: "w4m5", ...opts } as never) as unknown as string;
}