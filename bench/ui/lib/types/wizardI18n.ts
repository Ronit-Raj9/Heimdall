// Types the "wizard" namespace (lane U09) against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		wizard: typeof import("@/locales/en/wizard.json");
	}
}

export {};
