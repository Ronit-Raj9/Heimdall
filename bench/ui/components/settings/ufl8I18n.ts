import type ufl8En from "@/locales/en/ufl8.json";

// Types the "ufl8" namespace (locales/en/ufl8.json): round 5 lane L8, checks, monitors, operations and admin.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufl8: typeof ufl8En;
	}
}

export {};