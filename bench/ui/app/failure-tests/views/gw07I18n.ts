import type gw07En from "@/locales/en/gw07.json";

// Types the "gw07" namespace (locales/en/gw07.json): fix wave 2 lane G07 strings of Failure tests and the benchmark page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw07: typeof gw07En;
	}
}

export {};