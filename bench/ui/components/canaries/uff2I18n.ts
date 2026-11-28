import type uff2En from "@/locales/en/uff2.json";

// Types the "uff2" namespace (locales/en/uff2.json): round 4 lane F2, repeat runs, the model watch and drift monitors.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uff2: typeof uff2En;
	}
}

export {};