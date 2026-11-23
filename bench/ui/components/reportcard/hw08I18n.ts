import type hw08En from "@/locales/en/hw08.json";

// Types the "hw08" namespace (locales/en/hw08.json): thrown-out runs on the report card and the grader line on the run page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw08: typeof hw08En;
	}
}

export {};