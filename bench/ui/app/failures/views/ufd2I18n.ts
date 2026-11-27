import type ufd2En from "@/locales/en/ufd2.json";

// Types the "ufd2" namespace (locales/en/ufd2.json): the one Failures page (M7-1).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufd2: typeof ufd2En;
	}
}

export {};
