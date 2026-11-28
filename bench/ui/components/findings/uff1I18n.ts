import type uff1En from "@/locales/en/uff1.json";

// Types the "uff1" namespace (locales/en/uff1.json): round 4 lane F1, Find out why, Check fix and Draft with a model.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uff1: typeof uff1En;
	}
}

export {};
