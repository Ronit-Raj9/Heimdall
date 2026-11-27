import type uf5bEn from "@/locales/en/uf5b.json";

// Types the "uf5b" namespace (locales/en/uf5b.json): UI fix wave lane 5b strings of the runs list,
// Failure tests, MCP test, Replays and the compare pair picker.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf5b: typeof uf5bEn;
	}
}

export {};
