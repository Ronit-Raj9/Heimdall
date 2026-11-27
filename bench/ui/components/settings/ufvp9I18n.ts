import type ufvp9En from "@/locales/en/ufvp9.json";

// Types the "ufvp9" namespace (locales/en/ufvp9.json): round 3 lane P9, Gateway and Settings.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufvp9: typeof ufvp9En;
	}
}

export {};
