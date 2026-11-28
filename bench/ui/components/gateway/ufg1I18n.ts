import type ufg1En from "@/locales/en/ufg1.json";

// Types the "ufg1" namespace (locales/en/ufg1.json): round 6 lane G1, feature test states.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufg1: typeof ufg1En;
	}
}

export {};