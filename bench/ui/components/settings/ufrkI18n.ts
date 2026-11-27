import type ufrkEn from "@/locales/en/ufrk.json";

// Types the "ufrk" namespace (locales/en/ufrk.json): round 2 lane RK (settings, workers, model keys, share links).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufrk: typeof ufrkEn;
	}
}

export {};
