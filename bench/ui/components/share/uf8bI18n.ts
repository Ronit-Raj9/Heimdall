import type uf8bEn from "@/locales/en/uf8b.json";

// Types the "uf8b" namespace (locales/en/uf8b.json): docs, share dialog and shared page, not found page, job labels.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf8b: typeof uf8bEn;
	}
}

export {};
