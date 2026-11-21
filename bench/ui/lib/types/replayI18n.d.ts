// Types the "replay" i18n namespace from its catalog (lib/i18n/resources.ts LaneResources).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		replay: typeof import("@/locales/en/replay.json");
	}
}
export {};
