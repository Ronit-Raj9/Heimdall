import type ufl6En from "@/locales/en/ufl6.json";

// Types the "ufl6" namespace (locales/en/ufl6.json): round 5 lane L6, agents and Connect.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufl6: typeof ufl6En;
	}
}

export {};