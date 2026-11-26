import type w4intEn from "@/locales/en/w4int.json";

// Types the "w4int" namespace (locales/en/w4int.json): texts the final integration of wave 4 added.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4int: typeof w4intEn;
	}
}

export {};