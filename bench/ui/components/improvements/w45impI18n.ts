import type w45impEn from "@/locales/en/w45imp.json";

// Types the "w45imp" namespace (locales/en/w45imp.json): the improvement run form with its estimate,
// the verdict card, continue and resume, the timeline and the explain view (wave 4, lane I43).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w45imp: typeof w45impEn;
	}
}

export {};