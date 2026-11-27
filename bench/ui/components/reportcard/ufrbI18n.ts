import type ufrbEn from "@/locales/en/ufrb.json";

// Types the "ufrb" namespace (locales/en/ufrb.json): round 2 lane RB, the evaluation page, compare runs and the leaderboard.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufrb: typeof ufrbEn;
	}
}

export {};
