import type ufintEn from "@/locales/en/ufint.json";

// Types the "ufint" namespace (locales/en/ufint.json): texts added by the final integration pass.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufint: typeof ufintEn;
	}
}

export {};
