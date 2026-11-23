import type hw12En from "@/locales/en/hw12.json";

// Types the "hw12" namespace (locales/en/hw12.json): the comparison page header of fix wave 3 lane H12.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw12: typeof hw12En;
	}
}

export {};