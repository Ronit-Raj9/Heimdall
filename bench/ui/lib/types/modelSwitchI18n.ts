// Types the "modelSwitch" namespace (lane Z21: compare models for one agent, the model switch
// report and the Bifrost routing export) against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		modelSwitch: typeof import("@/locales/en/modelSwitch.json");
	}
}

export {};
