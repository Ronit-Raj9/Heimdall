import type w441En from "@/locales/en/w441.json";

// Types the "w441" namespace (locales/en/w441.json): first divergence and behaviour checks.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w441: typeof w441En;
	}
}

export {};