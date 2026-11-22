import type gw09En from "@/locales/en/gw09.json";

// Types the "gw09" namespace (locales/en/gw09.json): wizard deep links, the step 1 selected agent bar and the step 2
// version notes (fix wave 2, lane G09).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw09: typeof gw09En;
	}
}

export {};