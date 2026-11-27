import type uf2bEn from "@/locales/en/uf2b.json";

// Types the "uf2b" namespace (locales/en/uf2b.json): fix wave lane 2b texts for New agent, Connect,
// From traces and Improvements.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf2b: typeof uf2bEn;
	}
}

export {};
