import type uf4bEn from "@/locales/en/uf4b.json";

// Types the "uf4b" namespace (locales/en/uf4b.json): lane 4b texts for compare, comparisons, validity and leaderboard.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf4b: typeof uf4bEn;
	}
}

export {};
