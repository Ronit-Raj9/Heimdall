import type benchEn from "@/locales/en/bench.json";
import type platformEn from "@/locales/en/platform.json";

// Every locales/en/<ns>.json is a namespace, loaded with import.meta.glob (namespace = file
// name), so a lane adds its own catalog without editing this file. "bench" holds the benchmark
// screens, "platform" the hosted platform (sign-in, settings, jobs, the org switcher).
const modules = import.meta.glob<{ default: Record<string, unknown> }>("../../locales/en/*.json", { eager: true });

/**
 * Typed namespaces beyond bench and platform. A lane types its catalog from its own file:
 *   declare module "@/lib/i18n/resources" { interface LaneResources { models: typeof import("@/locales/en/models.json") } }
 */
// eslint-disable-next-line @typescript-eslint/no-empty-object-type
export interface LaneResources {}

export type EnResources = { bench: typeof benchEn; platform: typeof platformEn } & LaneResources;

const en: Record<string, Record<string, unknown>> = {};
for (const [path, mod] of Object.entries(modules)) {
	const ns = path.slice(path.lastIndexOf("/") + 1, -".json".length);
	en[ns] = mod.default;
}

export const NAMESPACES = Object.keys(en).sort() as (keyof EnResources)[];

export type I18nNamespace = keyof EnResources;

// English is bundled and is the source of the typed keys.
export const resources = { en: en as unknown as EnResources } as const;
