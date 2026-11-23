import type hw05En from "@/locales/en/hw05.json";

// Types the "hw05" namespace (locales/en/hw05.json): the "where runs go" confirmation and the own
// worker remove confirmation (fix wave 3, N217).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw05: typeof hw05En;
	}
}

export {};