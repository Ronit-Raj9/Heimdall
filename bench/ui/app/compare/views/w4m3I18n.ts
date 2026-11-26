import type w4m3En from "@/locales/en/w4m3.json";

// Types the "w4m3" namespace (locales/en/w4m3.json): the comparison minors of wave 4.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4m3: typeof w4m3En;
	}
}

export {};