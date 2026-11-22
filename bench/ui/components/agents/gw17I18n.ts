import type gw17En from "@/locales/en/gw17.json";

// Types the "gw17" namespace (locales/en/gw17.json): agent name problems and the report card's version note.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw17: typeof gw17En;
	}
}

export {};