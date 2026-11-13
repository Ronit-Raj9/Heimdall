import benchEn from "@/locales/en/bench.json";

// One namespace for now. When the screens move into ui/, "bench" joins ui/'s NAMESPACES and
// gets a catalog in every locale directory there.
export const NAMESPACES = ["bench"] as const;

export type I18nNamespace = (typeof NAMESPACES)[number];

// English is bundled and is the source of the typed keys.
export const resources = {
	en: {
		bench: benchEn,
	},
} as const;