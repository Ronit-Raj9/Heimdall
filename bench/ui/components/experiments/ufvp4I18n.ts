import type ufvp4En from "@/locales/en/ufvp4.json";

// Types the "ufvp4" namespace (locales/en/ufvp4.json): round 3 lane P4, the comparison pages.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufvp4: typeof ufvp4En;
	}
}

export {};
