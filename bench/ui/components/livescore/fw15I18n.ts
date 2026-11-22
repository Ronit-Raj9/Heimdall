import type fw15En from "@/locales/en/fw15.json";

// Types the "fw15" namespace (locales/en/fw15.json): the simulated live scoring try on the Live page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		fw15: typeof fw15En;
	}
}

export {};
