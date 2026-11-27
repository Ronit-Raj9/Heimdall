import type uf7bEn from "@/locales/en/uf7b.json";

// Types the "uf7b" namespace (locales/en/uf7b.json): checks and releases fix wave lane 7b.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf7b: typeof uf7bEn;
	}
}

export {};
