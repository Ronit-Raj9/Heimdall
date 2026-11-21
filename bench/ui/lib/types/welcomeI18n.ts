// Types the "welcome" namespace (lane L21) against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		welcome: typeof import("@/locales/en/welcome.json");
	}
}

export {};
