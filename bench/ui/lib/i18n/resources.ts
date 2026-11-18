import benchEn from "@/locales/en/bench.json";
import platformEn from "@/locales/en/platform.json";

// "bench" holds the benchmark screens. "platform" holds the hosted platform: sign-in, settings,
// jobs and the org switcher. When the screens move into ui/, both join ui/'s NAMESPACES and get a
// catalog in every locale directory there.
export const NAMESPACES = ["bench", "platform"] as const;

export type I18nNamespace = (typeof NAMESPACES)[number];

// English is bundled and is the source of the typed keys.
export const resources = {
	en: {
		bench: benchEn,
		platform: platformEn,
	},
} as const;