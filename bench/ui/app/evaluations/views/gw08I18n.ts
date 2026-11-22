import type gw08En from "@/locales/en/gw08.json";

// Types the "gw08" namespace (locales/en/gw08.json): past failure tests (fault changes, why a test has no results, stopped tests).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw08: typeof gw08En;
	}
}

export {};