import type w447En from "@/locales/en/w447.json";

// Types the "w447" namespace (locales/en/w447.json), owned by the shadow replay page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w447: typeof w447En;
	}
}

export {};