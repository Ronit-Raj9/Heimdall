import type gw12En from "@/locales/en/gw12.json";

// Types the "gw12" namespace (locales/en/gw12.json): benchmark list freshness and readiness words (fix wave 2, G12).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw12: typeof gw12En;
	}
}

export {};