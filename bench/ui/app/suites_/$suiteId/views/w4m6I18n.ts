import type w4m6En from "@/locales/en/w4m6.json";

// Types the "w4m6" namespace (locales/en/w4m6.json): the benchmark page tabs and export.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4m6: typeof w4m6En;
	}
}

export {};