import type uf7cEn from "@/locales/en/uf7c.json";

// Types the "uf7c" namespace (locales/en/uf7c.json): texts of the Gateway page fix wave.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf7c: typeof uf7cEn;
	}
}

export {};
