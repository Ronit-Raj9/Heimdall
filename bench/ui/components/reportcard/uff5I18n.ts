import type uff5En from "@/locales/en/uff5.json";

// Types the "uff5" namespace (locales/en/uff5.json): round 4 lane F5, caps and costs on evaluations and honest results.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uff5: typeof uff5En;
	}
}

export {};
