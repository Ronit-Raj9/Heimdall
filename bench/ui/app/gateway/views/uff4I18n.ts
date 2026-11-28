import type uff4En from "@/locales/en/uff4.json";

// Types the "uff4" namespace (locales/en/uff4.json): round 4 lane F4, the Bifrost feature test form and results.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uff4: typeof uff4En;
	}
}

export {};
