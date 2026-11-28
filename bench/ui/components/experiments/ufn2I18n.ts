import type ufn2En from "@/locales/en/ufn2.json";

// Types the "ufn2" namespace (locales/en/ufn2.json): round 6 lane N2, cap change confirm, waiting label.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufn2: typeof ufn2En;
	}
}

export {};