import type fw09En from "@/locales/en/fw09.json";

// Types the "fw09" namespace (locales/en/fw09.json): report card exports and the release title.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		fw09: typeof fw09En;
	}
}

export {};