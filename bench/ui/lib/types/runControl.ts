// Run control (handlers/runcontrol.go): translation namespace for pause, live settings, the error
// policy table, the change log, run cancel and requeue, and reruns of failures.

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		runControl: typeof import("@/locales/en/runControl.json");
	}
}

export {};
