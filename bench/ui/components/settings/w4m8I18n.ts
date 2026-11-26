import type w4m8En from "@/locales/en/w4m8.json";

// Types the "w4m8" namespace (locales/en/w4m8.json): wave 4 settings, usage, operations and share dialog fixes.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4m8: typeof w4m8En;
	}
}

export {};