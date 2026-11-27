import type uf5aEn from "@/locales/en/uf5a.json";

// Types the "uf5a" namespace (locales/en/uf5a.json): run page reason line, gates, header.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf5a: typeof uf5aEn;
	}
}

export {};
