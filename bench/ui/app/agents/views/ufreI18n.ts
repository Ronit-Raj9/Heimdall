import type ufreEn from "@/locales/en/ufre.json";

// Types the "ufre" namespace (locales/en/ufre.json): new texts of the round 2 Home and agents fixes.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufre: typeof ufreEn;
	}
}

export {};
