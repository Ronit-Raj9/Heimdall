import type hw10En from "@/locales/en/hw10.json";

// Types the "hw10" namespace (locales/en/hw10.json): the Model switch tab's release rule set line and
// select, and its routing file note.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw10: typeof hw10En;
	}
}

export {};