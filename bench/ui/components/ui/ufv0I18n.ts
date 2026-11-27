import type ufv0En from "@/locales/en/ufv0.json";

// Types the "ufv0" namespace (locales/en/ufv0.json): round 3 lane V0, the shared pieces.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufv0: typeof ufv0En;
	}
}

export {};
