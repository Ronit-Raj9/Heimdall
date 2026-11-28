import type uff6En from "@/locales/en/uff6.json";

// Types the "uff6" namespace (locales/en/uff6.json): round 4 lane F6, the small trial size of
// improvement runs.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uff6: typeof uff6En;
	}
}

export {};
