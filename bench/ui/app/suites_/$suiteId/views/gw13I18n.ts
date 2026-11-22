import type gw13En from "@/locales/en/gw13.json";

// Types the "gw13" namespace (locales/en/gw13.json): fix wave 2 lane G13 texts of the benchmark Tasks tab.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw13: typeof gw13En;
	}
}

export {};