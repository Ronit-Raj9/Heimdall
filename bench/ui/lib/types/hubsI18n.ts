// Types the "hubs" namespace (lane X09: the tabs of Benchmarks, Scheduled checks and Releases)
// against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hubs: typeof import("@/locales/en/hubs.json");
	}
}

export {};
