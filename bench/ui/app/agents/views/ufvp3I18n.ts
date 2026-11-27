import type ufvp3En from "@/locales/en/ufvp3.json";

// Types the "ufvp3" namespace (locales/en/ufvp3.json): round 3 visual pass, lane P3, the agents pages.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufvp3: typeof ufvp3En;
	}
}

export {};
