import type gw16En from "@/locales/en/gw16.json";

// Types the "gw16" namespace (locales/en/gw16.json): live scoring setup texts and the invitation page's
// signed-in answer (fix wave 2, lane G16).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw16: typeof gw16En;
	}
}

export {};