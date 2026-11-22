// Types the "recordings" i18n namespace (Z03) from its catalog (lib/i18n/resources.ts LaneResources).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		recordings: typeof import("@/locales/en/recordings.json");
	}
}
export {};
