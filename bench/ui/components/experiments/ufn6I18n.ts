import type ufn6En from "@/locales/en/ufn6.json";

// Types the "ufn6" namespace (locales/en/ufn6.json): round 6 lane N6, Extend runs pooled with the parent comparison.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufn6: typeof ufn6En;
	}
}

export {};
