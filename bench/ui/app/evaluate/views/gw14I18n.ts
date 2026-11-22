import type gw14En from "@/locales/en/gw14.json";

// Types the "gw14" namespace (locales/en/gw14.json): the runs at once line of wizard step 4 (fix wave 2, N90).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw14: typeof gw14En;
	}
}

export {};