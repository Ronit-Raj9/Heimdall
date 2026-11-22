// Types the "reference" namespace (gap G4-03: the reference board) against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		reference: typeof import("@/locales/en/reference.json");
	}
}

export {};
