import type gw06En from "@/locales/en/gw06.json";

// Types the "gw06" namespace (locales/en/gw06.json): one-setup notes, unscanned runs, release record badges and page links.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw06: typeof gw06En;
	}
}

export {};