import type w4m7En from "@/locales/en/w4m7.json";

// Types the "w4m7" namespace (locales/en/w4m7.json): wave 4 inbox, labelling, Maxim and monitor fixes.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4m7: typeof w4m7En;
	}
}

export {};