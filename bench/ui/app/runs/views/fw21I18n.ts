import type fw21En from "@/locales/en/fw21.json";

// Types the "fw21" namespace (locales/en/fw21.json), owned by the runs list.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		fw21: typeof fw21En;
	}
}

export {};