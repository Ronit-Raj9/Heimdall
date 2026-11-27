import type ufrfEn from "@/locales/en/ufrf.json";

// Types the "ufrf" namespace (locales/en/ufrf.json): round 2 lane RF, benchmarks and catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufrf: typeof ufrfEn;
	}
}

export {};
