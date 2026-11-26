import type w443En from "@/locales/en/w443.json";

// Types the "w443" namespace (locales/en/w443.json), owned by the Findings tab of the inbox.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w443: typeof w443En;
	}
}

export {};