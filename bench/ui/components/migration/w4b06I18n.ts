import type w4b06En from "@/locales/en/w4b06.json";

// Types the "w4b06" namespace (locales/en/w4b06.json): the Cost tab's answered-as line and the
// comparison builder's Estimate button while it waits.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4b06: typeof w4b06En;
	}
}

export {};