import type gw18En from "@/locales/en/gw18.json";

// Types the "gw18" namespace (locales/en/gw18.json): fix wave 2 lane G18 texts of the agent Versions tab.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw18: typeof gw18En;
	}
}

export {};