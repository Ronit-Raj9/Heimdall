import type ufd5En from "@/locales/en/ufd5.json";

// Types the "ufd5" namespace (locales/en/ufd5.json): archive, unarchive and the monitor test delete.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufd5: typeof ufd5En;
	}
}

export {};
