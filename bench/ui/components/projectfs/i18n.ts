import type filesEn from "@/locales/en/files.json";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		files: typeof filesEn;
	}
}

export const FILES_NS = "files" as const;
