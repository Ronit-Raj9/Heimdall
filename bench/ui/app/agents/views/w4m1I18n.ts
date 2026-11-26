import type w4m1En from "@/locales/en/w4m1.json";

// Types the "w4m1" namespace (locales/en/w4m1.json): wave 4 agents and Connect minors.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4m1: typeof w4m1En;
	}
}

export {};