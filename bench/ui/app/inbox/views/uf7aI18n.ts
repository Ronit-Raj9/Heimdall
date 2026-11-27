import type uf7aEn from "@/locales/en/uf7a.json";

// Types the "uf7a" namespace (locales/en/uf7a.json): texts of the failure area added in the UI fix wave, lane 7a.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf7a: typeof uf7aEn;
	}
}

export {};