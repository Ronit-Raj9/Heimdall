import type w4iwidEn from "@/locales/en/w4iwid.json";

// Types the "w4iwid" namespace (locales/en/w4iwid.json): the group D integrator's strings (row Delete on the
// benchmark and agent lists, N288 and X4).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4iwid: typeof w4iwidEn;
	}
}

export {};