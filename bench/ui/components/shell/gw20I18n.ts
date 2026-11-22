import type gw20En from "@/locales/en/gw20.json";

// Types the "gw20" namespace (locales/en/gw20.json): the tab strip's note for an unknown tab id.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw20: typeof gw20En;
	}
}

export {};