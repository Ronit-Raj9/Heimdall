import type hw16En from "@/locales/en/hw16.json";

// Types the "hw16" namespace (locales/en/hw16.json), owned by fix wave 3 lane H16.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw16: typeof hw16En;
	}
}

export {};