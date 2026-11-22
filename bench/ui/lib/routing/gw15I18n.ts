import type gw15En from "@/locales/en/gw15.json";

// Types the "gw15" namespace (locales/en/gw15.json): the not-found page's suggestions, the job
// page and the guides (fix wave 2, lane G15).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw15: typeof gw15En;
	}
}

export {};