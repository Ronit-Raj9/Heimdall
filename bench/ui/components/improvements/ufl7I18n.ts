import type ufl7En from "@/locales/en/ufl7.json";

// Types the "ufl7" namespace (locales/en/ufl7.json): round 5 lane L7, improvement studies, the
// chain check and one round in full.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufl7: typeof ufl7En;
	}
}

export {};