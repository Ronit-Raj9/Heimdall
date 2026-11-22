import type fw22En from "@/locales/en/fw22.json";

// Types the "fw22" namespace (locales/en/fw22.json): the signed-out quickstart page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		fw22: typeof fw22En;
	}
}

export {};