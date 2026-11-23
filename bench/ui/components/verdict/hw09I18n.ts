import type hw09En from "@/locales/en/hw09.json";

// Types the "hw09" namespace (locales/en/hw09.json): the setups verdict, the Result tab notes and
// the leaderboard cells of fix wave 3 lane H09.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw09: typeof hw09En;
	}
}

export {};