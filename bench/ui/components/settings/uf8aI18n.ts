import type uf8aEn from "@/locales/en/uf8a.json";

// Types the "uf8a" namespace (locales/en/uf8a.json): Settings shell and its tabs (fix wave lane 8a).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf8a: typeof uf8aEn;
	}
}

export {};