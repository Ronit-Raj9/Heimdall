import type ufc0En from "@/locales/en/ufc0.json";

// Types the "ufc0" namespace (locales/en/ufc0.json): texts of the shared pieces of phase C lane C0.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufc0: typeof ufc0En;
	}
}

export {};