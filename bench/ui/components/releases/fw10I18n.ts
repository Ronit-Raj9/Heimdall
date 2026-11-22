import type fw10En from "@/locales/en/fw10.json";

// Types the "fw10" namespace (locales/en/fw10.json): the release form's quick run toggle.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		fw10: typeof fw10En;
	}
}

export {};
