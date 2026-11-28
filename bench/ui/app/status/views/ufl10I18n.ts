import type ufl10En from "@/locales/en/ufl10.json";

// Types the "ufl10" namespace (locales/en/ufl10.json): round 5 lane L10, incident notes on the status page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufl10: typeof ufl10En;
	}
}

export {};