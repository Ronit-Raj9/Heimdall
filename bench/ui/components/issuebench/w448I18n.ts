import type w448En from "@/locales/en/w448.json";

// Types the "w448" namespace (locales/en/w448.json), owned by the weekly issue sync and the cheat rate row.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w448: typeof w448En;
	}
}

export {};