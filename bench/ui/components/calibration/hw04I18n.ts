import type hw04En from "@/locales/en/hw04.json";

// Types the "hw04" namespace (locales/en/hw04.json): fix wave 3 lane H04 (calibration labels, people shown by name).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw04: typeof hw04En;
	}
}

export {};