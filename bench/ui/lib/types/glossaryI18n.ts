// Types the "glossary" i18n namespace (lane P09) from its catalog (lib/i18n/resources.ts LaneResources).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		glossary: typeof import("@/locales/en/glossary.json");
	}
}
export {};
