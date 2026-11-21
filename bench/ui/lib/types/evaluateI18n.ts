// Types the "evaluate" namespace (lane A11, the /evaluate wizard) against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		evaluate: typeof import("@/locales/en/evaluate.json");
	}
}

export {};
