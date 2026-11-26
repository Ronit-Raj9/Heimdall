import type w4m2En from "@/locales/en/w4m2.json";

// Types the "w4m2" namespace (locales/en/w4m2.json): the evaluate wizard, the evaluation page, release evidence and failure tests.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4m2: typeof w4m2En;
	}
}

export {};