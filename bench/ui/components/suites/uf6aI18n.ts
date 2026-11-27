import type uf6aEn from "@/locales/en/uf6a.json";

// Types the "uf6a" namespace (locales/en/uf6a.json): UI fix wave lane 6a texts of the benchmark page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf6a: typeof uf6aEn;
	}
}

export {};
