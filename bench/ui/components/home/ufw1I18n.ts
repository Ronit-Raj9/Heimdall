import type ufw1En from "@/locales/en/ufw1.json";

// Types the "ufw1" namespace (locales/en/ufw1.json): round 2 pass W, lane W1 (Home, evaluate, evaluations, leaderboard).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufw1: typeof ufw1En;
	}
}

export {};
