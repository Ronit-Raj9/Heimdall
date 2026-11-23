import type hx01En from "@/locales/en/hx01.json";

// Types the "hx01" namespace (locales/en/hx01.json): which result the comparison page shows after a re-score.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hx01: typeof hx01En;
	}
}

export {};