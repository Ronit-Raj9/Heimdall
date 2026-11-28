import type ufl9En from "@/locales/en/ufl9.json";

// Types the "ufl9" namespace (locales/en/ufl9.json): round 5 lane L9, live scored sessions and leftovers.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufl9: typeof ufl9En;
	}
}

export {};