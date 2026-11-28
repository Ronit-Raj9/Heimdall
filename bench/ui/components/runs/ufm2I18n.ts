import type ufm2En from "@/locales/en/ufm2.json";

// Types the "ufm2" namespace (locales/en/ufm2.json): round 6 lane M2, skills in a run.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufm2: typeof ufm2En;
	}
}

export {};
