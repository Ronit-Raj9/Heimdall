import type ufl5En from "@/locales/en/ufl5.json";

// Types the "ufl5" namespace (locales/en/ufl5.json): round 5 lane L5, benchmarks and tasks.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufl5: typeof ufl5En;
	}
}

export {};
