import type ufrhEn from "@/locales/en/ufrh.json";

// Types the "ufrh" namespace (locales/en/ufrh.json): round 2 texts of the Failures page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufrh: typeof ufrhEn;
	}
}

export {};
