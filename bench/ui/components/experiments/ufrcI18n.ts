import type ufrcEn from "@/locales/en/ufrc.json";

// Types the "ufrc" namespace (locales/en/ufrc.json): round 2 lane RC texts for comparisons and the matrix.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufrc: typeof ufrcEn;
	}
}

export {};