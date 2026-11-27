import type uf6bEn from "@/locales/en/uf6b.json";

// Types the "uf6b" namespace (locales/en/uf6b.json): benchmarks list, add chooser, catalog and labelling.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf6b: typeof uf6bEn;
	}
}

export {};
