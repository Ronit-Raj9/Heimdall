import type ufk4En from "@/locales/en/ufk4.json";

// Types the "ufk4" namespace (locales/en/ufk4.json): round 6 lane K4, Draft with a model start errors.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufk4: typeof ufk4En;
	}
}

export {};
