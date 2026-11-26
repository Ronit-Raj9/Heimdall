import type w444En from "@/locales/en/w444.json";

// Types the "w444" namespace (locales/en/w444.json): task categories and the quality checks of generated tasks.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w444: typeof w444En;
	}
}

export {};