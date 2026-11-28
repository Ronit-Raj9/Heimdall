import type ufk2En from "@/locales/en/ufk2.json";

// Types the "ufk2" namespace (locales/en/ufk2.json): round 6 lane K2, step by step run comparison and per task reliability.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufk2: typeof ufk2En;
	}
}

export {};