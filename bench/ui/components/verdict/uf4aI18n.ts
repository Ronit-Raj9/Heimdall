import type uf4aEn from "@/locales/en/uf4a.json";

// Types the "uf4a" namespace (locales/en/uf4a.json): the comparison page fixes of UI fix wave lane 4a.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf4a: typeof uf4aEn;
	}
}

export {};