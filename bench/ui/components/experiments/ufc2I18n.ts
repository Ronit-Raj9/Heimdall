import type ufc2En from "@/locales/en/ufc2.json";

// Types the "ufc2" namespace (locales/en/ufc2.json): run controls, report card and wizard texts of fix wave lane C2.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufc2: typeof ufc2En;
	}
}

export {};
