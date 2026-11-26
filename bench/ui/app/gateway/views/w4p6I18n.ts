import type w4p6En from "@/locales/en/w4p6.json";

// Types the "w4p6" namespace (locales/en/w4p6.json), owned by the Gateway page and its cards.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4p6: typeof w4p6En;
	}
}

export {};