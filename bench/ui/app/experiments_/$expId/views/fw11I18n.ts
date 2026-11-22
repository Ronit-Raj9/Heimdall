import type fw11En from "@/locales/en/fw11.json";

// Types the "fw11" namespace (locales/en/fw11.json): the comparison page, its builder and re-score strings.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		fw11: typeof fw11En;
	}
}

export {};