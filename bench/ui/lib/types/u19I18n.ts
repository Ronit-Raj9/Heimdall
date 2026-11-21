// Types the "u19" namespace (lane U19: sign-in pages, guest banner, quickstart) against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		u19: typeof import("@/locales/en/u19.json");
	}
}

export {};
