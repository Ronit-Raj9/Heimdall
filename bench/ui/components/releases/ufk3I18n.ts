import type ufk3En from "@/locales/en/ufk3.json";

// Types the "ufk3" namespace (locales/en/ufk3.json): round 6 lane K3, labels and privacy on result pages.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufk3: typeof ufk3En;
	}
}

export {};