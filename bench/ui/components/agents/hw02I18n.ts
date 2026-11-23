import type hw02En from "@/locales/en/hw02.json";

// Types the "hw02" namespace (locales/en/hw02.json): the agent skill scan bar, benchmark delete and
// the new benchmark id check (fix wave 3, lane H02).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw02: typeof hw02En;
	}
}

export {};