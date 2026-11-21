// Types the "upload" i18n namespace from its catalog (lib/i18n/resources.ts LaneResources).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		upload: typeof import("@/locales/en/upload.json");
	}
}
export {};
