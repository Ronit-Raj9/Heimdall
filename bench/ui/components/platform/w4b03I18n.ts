import type w4b03En from "@/locales/en/w4b03.json";

// Types the "w4b03" namespace (locales/en/w4b03.json): the organization reset banner and the unknown agent page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4b03: typeof w4b03En;
	}
}

export {};