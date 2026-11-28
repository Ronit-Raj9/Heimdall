import type ufl1En from "@/locales/en/ufl1.json";

// Types the "ufl1" namespace (locales/en/ufl1.json): round 5 lane L1, the Gateway page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufl1: typeof ufl1En;
	}
}

export {};