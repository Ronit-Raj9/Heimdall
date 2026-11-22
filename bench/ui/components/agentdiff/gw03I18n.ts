import type gw03En from "@/locales/en/gw03.json";

// Types the "gw03" namespace (locales/en/gw03.json): the agent diff version picker words.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw03: typeof gw03En;
	}
}

export {};