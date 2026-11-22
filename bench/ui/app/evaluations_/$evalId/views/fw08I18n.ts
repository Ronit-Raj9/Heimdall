import type fw08En from "@/locales/en/fw08.json";

// Types the "fw08" namespace (locales/en/fw08.json): the evaluation page, its list and the run control names.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		fw08: typeof fw08En;
	}
}

export {};