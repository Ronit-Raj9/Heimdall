import type w4m0En from "@/locales/en/w4m0.json";

// Types the "w4m0" namespace (locales/en/w4m0.json): the shared wave 4 helpers.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4m0: typeof w4m0En;
	}
}

export {};
