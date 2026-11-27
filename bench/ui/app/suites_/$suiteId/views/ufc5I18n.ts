import type ufc5En from "@/locales/en/ufc5.json";

// Types the "ufc5" namespace (locales/en/ufc5.json): UI fix wave phase C lane 5 texts of the benchmark pages.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufc5: typeof ufc5En;
	}
}

export {};
