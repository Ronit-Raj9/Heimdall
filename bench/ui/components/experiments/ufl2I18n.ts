import type ufl2En from "@/locales/en/ufl2.json";

// Types the "ufl2" namespace (locales/en/ufl2.json): round 5 lane L2, the new comparison form and the comparison page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufl2: typeof ufl2En;
	}
}

export {};