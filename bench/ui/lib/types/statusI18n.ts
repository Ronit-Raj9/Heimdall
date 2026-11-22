// Types the "status" namespace (lane Z14: the public status page) against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		status: typeof import("@/locales/en/status.json");
	}
}

export {};
