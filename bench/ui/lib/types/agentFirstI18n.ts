// Types the "agentFirst" namespace (lane X11: agent page, Home agent cards, report card
// failures, Why agents fail, Failure tests) against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		agentFirst: typeof import("@/locales/en/agentFirst.json");
	}
}

export {};
