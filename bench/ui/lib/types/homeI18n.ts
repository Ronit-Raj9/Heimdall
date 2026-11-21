// Types the "home" namespace (lane U02) against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		home: typeof import("@/locales/en/home.json");
	}
}

export {};
