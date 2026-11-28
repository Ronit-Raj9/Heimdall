import type uft5En from "@/locales/en/uft5.json";

// Types the "uft5" namespace (locales/en/uft5.json): the From issues start form (cap, what runs, who pays).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uft5: typeof uft5En;
	}
}

export {};