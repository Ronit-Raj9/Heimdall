import type ufl4En from "@/locales/en/ufl4.json";

// Types the "ufl4" namespace (locales/en/ufl4.json): round 5 lane L4, the run page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufl4: typeof ufl4En;
	}
}

export {};